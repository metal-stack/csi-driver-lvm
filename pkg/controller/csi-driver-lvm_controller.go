package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
)

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// add kubebuilder annotations for RBAC
type CsiDriverLvmReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *CsiDriverLvmReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *CsiDriverLvmReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&corev1.Node{}).Complete(r)
}
