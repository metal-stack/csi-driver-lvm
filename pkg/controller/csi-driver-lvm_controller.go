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
		return ctrl.Result{}, fmt.Errorf("unable to fetch pvc %q: %w", pod.Spec.NodeName, err)
	}

	if !node.Spec.Unschedulable {
		return ctrl.Result{}, nil
	}

	//extract pvc-templates only once for the sts
	var claimTemplatesScs []string
	for _, or := range pod.OwnerReferences {
		if or.Kind != "StatefulSet" {
			continue
		}
		var sts appsv1.StatefulSet
		if err := r.Get(ctx, types.NamespacedName{Name: or.Name, Namespace: pod.Namespace}, &sts); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to fetch sts %q: %w", or.Name, err)
		}

		for _, claimTemplate := range sts.Spec.VolumeClaimTemplates {
			claimTemplatesScs = append(claimTemplatesScs, *claimTemplate.Spec.StorageClassName)
		}
	}

	// test if sts of pod has pvc
	if len(claimTemplatesScs) == 0 {
		return ctrl.Result{}, nil
	}

	//iterate over pod pvc and test if deletion on evict is allowed
	for _, volume := range pod.Spec.Volumes {
		var pvc corev1.PersistentVolumeClaim

		pvcName := volume.PersistentVolumeClaim
		if volume.PersistentVolumeClaim == nil {
			// skipping no pvc volumes
			continue
		}

		if err := r.Get(ctx, types.NamespacedName{Name: pvcName.ClaimName, Namespace: pod.Namespace}, &pvc); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to fetch pvc %q: %w", pvcName.ClaimName, err)
		}

		var sc storagev1.StorageClass
		if err := r.Get(ctx, types.NamespacedName{Name: *pvc.Spec.StorageClassName}, &sc); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to fetch sc %q: %w", *pvc.Spec.StorageClassName, err)
		}

		if sc.Provisioner != r.cfg.ProvisionerName {
			//skipping pvcs not provisioned by csi-driver-lvm
			continue
		}

		if !slices.Contains(claimTemplatesScs, sc.Name) {
			//skipping pvcs which don't contain a claim-template with a csi-driver-lvm scs
			continue
		}

		if value, ok := pvc.Annotations[isEvictionAllowedAnnotation]; ok {
			isEvictionAllowed, err := strconv.ParseBool(value)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("unable to parse annotation for %q: %w", isEvictionAllowedAnnotation, err)
			}

			if isEvictionAllowed {
				if err := r.Delete(ctx, &pvc); err != nil {
					return ctrl.Result{}, fmt.Errorf("unable to delete pvc %q: %w", pvc.Name, err)
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *CsiDriverLvmReconciler) SetupWithManager(mgr ctrl.Manager) error {
	updatePred := predicate.Funcs{
		// Only allow updates when pod gets evicted and is referenced by sts
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObj := e.ObjectOld.(*corev1.Pod)
			newObj := e.ObjectNew.(*corev1.Pod)

			hasOldObjDisruption := false
			hasNewObjDisruption := false

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
