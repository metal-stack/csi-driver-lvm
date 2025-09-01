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
	storagev1 "k8s.io/api/storage/v1"
)

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;delete
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

func (r *CsiDriverLvmReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to fetch pod %q: %w", req.NamespacedName, err)
	}

	var node corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, &node); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to fetch node %q: %w", pod.Spec.NodeName, err)
	}
	if !node.Spec.Unschedulable {
		return ctrl.Result{}, nil
	}

	// helper to parse
	parseBoolAnn := func(ann map[string]string, owner string) (bool, error) {
		if v, ok := ann[isEvictionAllowedAnnotation]; ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return false, fmt.Errorf("unable to parse %s annotation value for %q: %w", owner, isEvictionAllowedAnnotation, err)
			}
			return b, nil
		}
		return false, nil
	}

	podAllowed, err := parseBoolAnn(pod.Annotations, "pod")
	if err != nil {
		return ctrl.Result{}, err
	}

	// pvc-names of sts
	var belongs []string
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
			belongs = append(belongs, pvcName)
		}
		break
	}

	// helper to test provisioner of sc
	isManagedSC := func(scName *string) (bool, error) {
		if scName == nil {
			return false, nil
		}
		var sc storagev1.StorageClass
		if err := r.Get(ctx, types.NamespacedName{Name: *scName}, &sc); err != nil {
			return false, fmt.Errorf("unable to fetch sc %q: %w", *scName, err)
		}
		return sc.Provisioner == r.cfg.ProvisionerName, nil
	}

	// iterate over volumes of pod
	// 1. only pvc volumes
	// 2. test for managed sc of pvc
	// 3. test if pvc-name is in derivated pvc-names of sts
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

		managed, err := isManagedSC(pvc.Spec.StorageClassName)
		if err != nil {
			return ctrl.Result{}, err
		}

		if !managed {
			continue
		}

		pvcAllowed, err := parseBoolAnn(pvc.Annotations, "pvc")
		if err != nil {
			return ctrl.Result{}, err
		}

		pvcBelongsToSTS := slices.Contains(belongs, pvc.Name)
		allowed := pvcBelongsToSTS && (podAllowed || pvcAllowed)
		if !allowed {
			continue
		}

		r.Log.Info("trying to delete pvc because of eviction",
			"pvc", pvc.Name, "pod", pod.Name, "namespace", pvc.Namespace,
			"node", node.Name)

		if err := r.Delete(ctx, &pvc); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to delete pvc %q: %w", pvc.Name, err)
		}
		r.Log.Info("deleted PVC because of eviction", "pvc", pvc.Name, "pod", pod.Name, "node", node.Name)
	}
	return ctrl.Result{}, nil
}

func (r *CsiDriverLvmReconciler) SetupWithManager(mgr ctrl.Manager) error {
	updatePred := predicate.Funcs{
		// Only allow updates when pod gets evicted and is referenced by sts
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObj := e.ObjectOld.(*corev1.Pod)
			newObj := e.ObjectNew.(*corev1.Pod)

			hasOldObjDisruption := slices.ContainsFunc(oldObj.Status.Conditions, func(cond corev1.PodCondition) bool {
				return cond.Type == corev1.DisruptionTarget
			})
			hasNewObjDisruption := slices.ContainsFunc(newObj.Status.Conditions, func(cond corev1.PodCondition) bool {
				return cond.Type == corev1.DisruptionTarget
			})

			for _, cond := range oldObj.Status.Conditions {
				if cond.Type == corev1.DisruptionTarget {
					hasOldObjDisruption = true
				}
			}
			for _, cond := range newObj.Status.Conditions {
				if cond.Type == corev1.DisruptionTarget {
					hasNewObjDisruption = true
				}
			}

			if hasNewObjDisruption && !hasOldObjDisruption {
				for _, or := range newObj.OwnerReferences {
					if or.Kind == "StatefulSet" {
						return true
					}
				}
			}
			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
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
