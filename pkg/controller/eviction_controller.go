package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch;
// +kubebuilder:rbac:groups="apps",resources=statefulsets,verbs=get;list;watch
// +kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses,verbs=get;list;watch

const (
	isEvictionAllowedAnnotation = "metal-stack.io/csi-driver-lvm.is-eviction-allowed"
)

type Config struct {
	ProvisionerName string
}

type CsiDriverLvmReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger

	cfg Config
}

func New(client client.Client, scheme *runtime.Scheme, log logr.Logger, cfg Config) *CsiDriverLvmReconciler {
	return &CsiDriverLvmReconciler{
		Client: client,
		Scheme: scheme,
		Log:    log,
		cfg:    cfg,
	}
}

func parseBoolAnn(obj client.Object) (bool, error) {
	ann := obj.GetAnnotations()
	if v, ok := ann[isEvictionAllowedAnnotation]; ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("unable to parse %s annotation value for %q: %w",
				obj.GetName(), isEvictionAllowedAnnotation, err)
		}
		return b, nil
	}
	return false, nil
}

func isUnscheduled(conditions []corev1.PodCondition) bool {
	return slices.ContainsFunc(conditions, func(cond corev1.PodCondition) bool {
		return cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == "Unschedulable"
	})
}

func hasDisruptionCondition(conditions []corev1.PodCondition) bool {
	return slices.ContainsFunc(conditions, func(cond corev1.PodCondition) bool {
		return cond.Type == corev1.DisruptionTarget
	})

}

func (r *CsiDriverLvmReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to fetch pod %q: %w", req.NamespacedName, err)
	}

	belongs := map[string]bool{}
	for _, or := range pod.OwnerReferences {
		if or.Kind != "StatefulSet" {
			continue
		}
		var sts appsv1.StatefulSet
		if err := r.Get(ctx, types.NamespacedName{Name: or.Name, Namespace: pod.Namespace}, &sts); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to fetch sts %q: %w", or.Name, err)
		}
		for _, ct := range sts.Spec.VolumeClaimTemplates {
			pvcName := ct.Name + "-" + pod.Name
			belongs[pvcName] = true
		}
	}

	// iterate over volumes of pod
	// 1. only pvc volumes
	// 2. test for managed sc of pvc
	// 3. test if pvc-name is in derived pvc-names of sts
	// 4. delete pvc only if belongs to sts and annotation is set on pod or pvc
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}

		var pvc corev1.PersistentVolumeClaim
		if err := r.Get(ctx, types.NamespacedName{
			Name:      volume.PersistentVolumeClaim.ClaimName,
			Namespace: pod.Namespace,
		}, &pvc); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to fetch pvc %q: %w", volume.PersistentVolumeClaim.ClaimName, err)
		}

		pvcAllowed, err := parseBoolAnn(&pvc)
		if err != nil {
			return ctrl.Result{}, err
		}

		podAllowed, err := parseBoolAnn(&pod)
		if err != nil {
			return ctrl.Result{}, err
		}

		if !podAllowed && !pvcAllowed {
			continue
		}

		if _, ok := belongs[pvc.Name]; !ok {
			continue
		}

		var pv corev1.PersistentVolume
		if err := r.Get(ctx, types.NamespacedName{
			Name: pvc.Spec.VolumeName,
		}, &pv); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to fetch pv %q: %w", pvc.Spec.VolumeName, err)
		}
		if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != r.cfg.ProvisionerName {
			continue
		}

		if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
			// no node affinity on the pv => we do not need to delete anything
			continue
		}

		if len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms) != 1 {
			return ctrl.Result{}, fmt.Errorf("unexpected node-affinity in pv in csi-driver-lvm managed pv %s", pv.Name)
		}

		var node corev1.Node
		err = r.Get(ctx, types.NamespacedName{Name: pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values[0]}, &node)
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("unable to fetch node %q: %w", pod.Spec.NodeName, err)
		}

		if err == nil && !node.Spec.Unschedulable {
			// as long as the node is present and schedulable, we do not need to delete the pvc
			// this can happen for instance on pod eviction through a pod autoscaler
			continue
		}

		r.Log.Info("trying to delete pvc because of eviction",
			"pvc", pvc.Name, "pod", pod.Name, "namespace", pvc.Namespace,
		)

		if err := r.Delete(ctx, &pvc); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to delete pvc %q: %w", pvc.Name, err)
		}
		r.Log.Info("deleted PVC because of eviction", "pvc", pvc.Name, "pod", pod.Name)
	}
	return ctrl.Result{}, nil
}

// Mechanism only works for StatefulSets with volumeClaimTemplates
func (r *CsiDriverLvmReconciler) SetupWithManager(mgr ctrl.Manager) error {
	updatePred := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			// Only allow updates when pod gets evicted and is referenced by sts
			newObj := e.ObjectNew.(*corev1.Pod)

			if hasDisruptionCondition(newObj.Status.Conditions) {
				for _, or := range newObj.OwnerReferences {
					if or.Kind == "StatefulSet" {
						return true
					}
				}
			}
			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			// These events are received on controller start and can be used to find unscheduled pods
			newObj := e.Object.(*corev1.Pod)

			if isUnscheduled(newObj.Status.Conditions) {
				for _, or := range newObj.OwnerReferences {
					if or.Kind == "StatefulSet" {
						return true
					}
				}
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}, builder.WithPredicates(updatePred)).
		Complete(r)
}
