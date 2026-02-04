package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	v1alpha1 "github.com/metal-stack/csi-driver-lvm/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// +kubebuilder:rbac:groups=lvm.csi.metal-stack.io,resources=drbdvolumes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=lvm.csi.metal-stack.io,resources=drbdvolumes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=csistoragecapacities,verbs=get;list;watch

const (
	// DegradedGracePeriod is how long a volume must remain in Degraded phase
	// before re-replication is triggered. This prevents premature re-replication
	// during rolling updates where nodes are temporarily NotReady.
	DegradedGracePeriod = 5 * time.Minute
)

type DRBDReplicationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger

	ProvisionerName string
}

func NewDRBDReplicationReconciler(c client.Client, scheme *runtime.Scheme, log logr.Logger, provisionerName string) *DRBDReplicationReconciler {
	return &DRBDReplicationReconciler{
		Client:          c,
		Scheme:          scheme,
		Log:             log,
		ProvisionerName: provisionerName,
	}
}

func (r *DRBDReplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("drbdvolume", req.Name)

	var dv v1alpha1.DRBDVolume
	if err := r.Get(ctx, req.NamespacedName, &dv); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip volumes being deleted
	if dv.Status.Phase == v1alpha1.VolumePhaseDeleting {
		return ctrl.Result{}, nil
	}

	// Phase 1: Assign secondary node if not set
	if dv.Spec.SecondaryNode == "" {
		log.Info("selecting secondary node for drbd volume")
		secondary, err := r.selectSecondaryNode(ctx, dv.Spec.PrimaryNode, dv.Spec.SizeBytes)
		if err != nil {
			log.Error(err, "failed to select secondary node")
			return ctrl.Result{}, err
		}

		if secondary == "" {
			log.Info("no suitable secondary node found, will retry")
			return ctrl.Result{Requeue: true}, nil
		}

		dv.Spec.SecondaryNode = secondary
		if err := r.Update(ctx, &dv); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update drbdvolume with secondary node: %w", err)
		}

		dv.Status.Phase = v1alpha1.VolumePhaseSecondaryAssigned
		if err := r.Status().Update(ctx, &dv); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update drbdvolume status: %w", err)
		}

		log.Info("assigned secondary node", "secondary", secondary)
		return ctrl.Result{}, nil
	}

	// Phase 2: Handle degraded volumes — check if secondary node is gone and re-replicate
	if dv.Status.Phase == v1alpha1.VolumePhaseDegraded {
		// Set DegradedSince if not already set (first time entering this path)
		if dv.Status.DegradedSince == nil {
			now := metav1.Now()
			dv.Status.DegradedSince = &now
			if err := r.Status().Update(ctx, &dv); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set degradedSince timestamp: %w", err)
			}
			log.Info("recorded degraded timestamp, will wait for grace period before re-replication",
				"degradedSince", now.Time, "gracePeriod", DegradedGracePeriod)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		secondaryGone, err := r.isNodeGone(ctx, dv.Spec.SecondaryNode)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to check secondary node status: %w", err)
		}

		if secondaryGone {
			// Enforce grace period: only re-replicate after DegradedGracePeriod has elapsed.
			// This prevents premature re-replication during rolling updates where nodes
			// are temporarily NotReady while rebooting.
			elapsed := time.Since(dv.Status.DegradedSince.Time)
			if elapsed < DegradedGracePeriod {
				remaining := DegradedGracePeriod - elapsed
				log.Info("secondary node appears gone but grace period has not elapsed, waiting",
					"secondary", dv.Spec.SecondaryNode,
					"degradedSince", dv.Status.DegradedSince.Time,
					"remaining", remaining,
				)
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}

			log.Info("secondary node is gone and grace period elapsed, selecting replacement",
				"old-secondary", dv.Spec.SecondaryNode,
				"degradedSince", dv.Status.DegradedSince.Time,
			)

			newSecondary, err := r.selectSecondaryNode(ctx, dv.Spec.PrimaryNode, dv.Spec.SizeBytes)
			if err != nil {
				log.Error(err, "failed to select replacement secondary node")
				return ctrl.Result{}, err
			}

			if newSecondary == "" {
				log.Info("no suitable replacement secondary node found, will retry")
				return ctrl.Result{Requeue: true}, nil
			}

			oldSecondary := dv.Spec.SecondaryNode
			dv.Spec.SecondaryNode = newSecondary
			if err := r.Update(ctx, &dv); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update drbdvolume with replacement secondary: %w", err)
			}

			// Reset readiness flags so both sides re-establish DRBD
			dv.Status.SecondaryReady = false
			dv.Status.PrimaryReady = false
			dv.Status.DegradedSince = nil
			dv.Status.Phase = v1alpha1.VolumePhaseSecondaryAssigned
			if err := r.Status().Update(ctx, &dv); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update drbdvolume status for re-replication: %w", err)
			}

			log.Info("assigned replacement secondary node",
				"old-secondary", oldSecondary,
				"new-secondary", newSecondary,
			)
			return ctrl.Result{}, nil
		}

		// Secondary node still exists — it may recover on its own.
		// If it recovers and DRBD reconnects, the volume should transition back to Established.
		log.Info("secondary node still exists, waiting for it to recover", "secondary", dv.Spec.SecondaryNode)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Phase 3: Update phase based on readiness
	if dv.Status.PrimaryReady && dv.Status.SecondaryReady && dv.Status.Phase != v1alpha1.VolumePhaseEstablished {
		dv.Status.Phase = v1alpha1.VolumePhaseEstablished
		dv.Status.DegradedSince = nil
		if err := r.Status().Update(ctx, &dv); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update drbdvolume status to established: %w", err)
		}
		log.Info("drbd volume is established")
	}

	return ctrl.Result{}, nil
}

// isNodeGone returns true if the node does not exist or has been marked unschedulable
// and has no Ready condition (i.e. it's truly gone, not just cordoned for maintenance).
func (r *DRBDReplicationReconciler) isNodeGone(ctx context.Context, nodeName string) (bool, error) {
	var node corev1.Node
	err := r.Get(ctx, types.NamespacedName{Name: nodeName}, &node)
	if err != nil {
		// Node object doesn't exist — it's been removed from the cluster
		if client.IgnoreNotFound(err) == nil {
			return true, nil
		}
		return false, err
	}

	// Node exists but check if it's both unschedulable and not Ready
	// (a cordoned node that's still running should not trigger re-replication)
	if !node.Spec.Unschedulable {
		return false, nil
	}

	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			if cond.Status == corev1.ConditionTrue {
				// Node is cordoned but healthy — don't re-replicate yet
				return false, nil
			}
			// Node is unschedulable and NotReady — consider it gone
			return true, nil
		}
	}

	// No Ready condition found and unschedulable — consider it gone
	return true, nil
}

// nodeCapacity holds capacity data for ranking nodes.
type nodeCapacity struct {
	NodeName      string
	AvailableBytes int64
	ReplicaCount  int
}

// selectSecondaryNode selects the best secondary node using a least-usage heuristic.
// It examines CSIStorageCapacity objects for available space and counts existing
// DRBD secondary assignments to prefer nodes with fewer replicas.
func (r *DRBDReplicationReconciler) selectSecondaryNode(ctx context.Context, primaryNode string, requiredBytes int64) (string, error) {
	// List all nodes
	var nodeList corev1.NodeList
	if err := r.List(ctx, &nodeList); err != nil {
		return "", fmt.Errorf("failed to list nodes: %w", err)
	}

	// Build set of schedulable nodes (excluding primary)
	schedulableNodes := map[string]bool{}
	for _, node := range nodeList.Items {
		if node.Name == primaryNode {
			continue
		}
		if node.Spec.Unschedulable {
			continue
		}
		schedulableNodes[node.Name] = true
	}

	if len(schedulableNodes) == 0 {
		return "", nil
	}

	// Get storage capacities for our provisioner
	var capacities storagev1.CSIStorageCapacityList
	if err := r.List(ctx, &capacities); err != nil {
		return "", fmt.Errorf("failed to list csi storage capacities: %w", err)
	}

	nodeCapacities := map[string]int64{}
	for _, cap := range capacities.Items {
		if cap.StorageClassName == "" {
			continue
		}

		// Extract node name from topology
		if cap.NodeTopology == nil {
			continue
		}

		for _, expr := range cap.NodeTopology.MatchLabels {
			_ = expr // matchLabels iteration
		}

		// CSIStorageCapacity uses NodeTopology as a label selector
		// The provisioner sets topology.lvm.csi/node=<nodename>
		nodeName := ""
		if cap.NodeTopology != nil {
			for k, v := range cap.NodeTopology.MatchLabels {
				if k == "topology.lvm.csi/node" {
					nodeName = v
					break
				}
			}
		}

		if nodeName == "" || !schedulableNodes[nodeName] {
			continue
		}

		if cap.Capacity != nil {
			bytes := cap.Capacity.Value()
			if bytes > nodeCapacities[nodeName] {
				nodeCapacities[nodeName] = bytes
			}
		}
	}

	// Count existing DRBD secondary assignments per node
	var dvList v1alpha1.DRBDVolumeList
	if err := r.List(ctx, &dvList); err != nil {
		return "", fmt.Errorf("failed to list drbd volumes: %w", err)
	}

	replicaCounts := map[string]int{}
	for _, dv := range dvList.Items {
		if dv.Spec.SecondaryNode != "" {
			replicaCounts[dv.Spec.SecondaryNode]++
		}
	}

	// Build candidates with scoring
	var candidates []nodeCapacity
	for nodeName := range schedulableNodes {
		avail, hasCapacity := nodeCapacities[nodeName]
		if !hasCapacity {
			// If no capacity data, skip — we can't verify the node has enough space
			continue
		}

		if avail < requiredBytes {
			continue
		}

		candidates = append(candidates, nodeCapacity{
			NodeName:       nodeName,
			AvailableBytes: avail,
			ReplicaCount:   replicaCounts[nodeName],
		})
	}

	if len(candidates) == 0 {
		// Fallback: if no capacity data available, pick any schedulable node with fewest replicas
		for nodeName := range schedulableNodes {
			candidates = append(candidates, nodeCapacity{
				NodeName:       nodeName,
				AvailableBytes: 0,
				ReplicaCount:   replicaCounts[nodeName],
			})
		}
	}

	if len(candidates) == 0 {
		return "", nil
	}

	// Sort: least replicas first, then most available capacity
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ReplicaCount != candidates[j].ReplicaCount {
			return candidates[i].ReplicaCount < candidates[j].ReplicaCount
		}
		return candidates[i].AvailableBytes > candidates[j].AvailableBytes
	})

	return candidates[0].NodeName, nil
}

// HandleFailover performs the failover of a DRBD volume from its current primary
// to its secondary. It updates the PV node affinity and swaps primary/secondary.
func (r *DRBDReplicationReconciler) HandleFailover(ctx context.Context, dvName string) error {
	log := r.Log.WithValues("drbdvolume", dvName)

	var dv v1alpha1.DRBDVolume
	if err := r.Get(ctx, types.NamespacedName{Name: dvName}, &dv); err != nil {
		return fmt.Errorf("failed to get drbdvolume %s: %w", dvName, err)
	}

	if dv.Spec.SecondaryNode == "" {
		return fmt.Errorf("drbdvolume %s has no secondary node for failover", dvName)
	}

	oldPrimary := dv.Spec.PrimaryNode
	newPrimary := dv.Spec.SecondaryNode

	log.Info("performing failover", "old-primary", oldPrimary, "new-primary", newPrimary)

	// Update the PV node affinity to point to the new primary
	var pvList corev1.PersistentVolumeList
	if err := r.List(ctx, &pvList); err != nil {
		return fmt.Errorf("failed to list pvs: %w", err)
	}

	for i := range pvList.Items {
		pv := &pvList.Items[i]
		if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != r.ProvisionerName {
			continue
		}
		if pv.Spec.CSI.VolumeHandle != dv.Spec.VolumeName {
			continue
		}

		// Update node affinity
		if pv.Spec.NodeAffinity != nil && pv.Spec.NodeAffinity.Required != nil {
			for j := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
				for k := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms[j].MatchExpressions {
					expr := &pv.Spec.NodeAffinity.Required.NodeSelectorTerms[j].MatchExpressions[k]
					if expr.Key == "topology.lvm.csi/node" {
						expr.Values = []string{newPrimary}
					}
				}
			}
		}

		if err := r.Update(ctx, pv); err != nil {
			return fmt.Errorf("failed to update pv %s node affinity: %w", pv.Name, err)
		}

		log.Info("updated pv node affinity", "pv", pv.Name, "new-node", newPrimary)
	}

	// Swap primary and secondary in the DRBDVolume
	dv.Spec.PrimaryNode = newPrimary
	dv.Spec.SecondaryNode = oldPrimary
	if err := r.Update(ctx, &dv); err != nil {
		return fmt.Errorf("failed to update drbdvolume spec: %w", err)
	}

	now := metav1.Now()
	dv.Status.Phase = v1alpha1.VolumePhaseDegraded
	dv.Status.DegradedSince = &now
	if err := r.Status().Update(ctx, &dv); err != nil {
		return fmt.Errorf("failed to update drbdvolume status: %w", err)
	}

	log.Info("failover complete", "new-primary", newPrimary)
	return nil
}

func (r *DRBDReplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pred := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.DRBDVolume{}).
		WithEventFilter(pred).
		Complete(r)
}
