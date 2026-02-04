package nodeagent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	v1alpha1 "github.com/metal-stack/csi-driver-lvm/api/v1alpha1"
	"github.com/metal-stack/csi-driver-lvm/pkg/drbd"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Agent runs on each node and handles DRBD operations for DRBDVolume CRs
// that reference this node as either primary or secondary.
type Agent struct {
	log      *slog.Logger
	client   client.Client
	nodeID   string
	vgName   string
	pollInterval time.Duration
}

func New(log *slog.Logger, c client.Client, nodeID, vgName string) *Agent {
	return &Agent{
		log:          log,
		client:       c,
		nodeID:       nodeID,
		vgName:       vgName,
		pollInterval: 10 * time.Second,
	}
}

// Run starts the polling loop that watches for DRBDVolume CRs assigned to this node.
func (a *Agent) Run(ctx context.Context) {
	a.log.Info("starting drbd node agent", "node", a.nodeID)

	wait.UntilWithContext(ctx, a.reconcileAll, a.pollInterval)

	a.log.Info("drbd node agent stopped")
}

func (a *Agent) reconcileAll(ctx context.Context) {
	var dvList v1alpha1.DRBDVolumeList
	if err := a.client.List(ctx, &dvList); err != nil {
		a.log.Error("failed to list drbd volumes", "error", err)
		return
	}

	for i := range dvList.Items {
		dv := &dvList.Items[i]
		if err := a.reconcile(ctx, dv); err != nil {
			a.log.Error("failed to reconcile drbd volume", "name", dv.Name, "error", err)
		}
	}
}

func (a *Agent) reconcile(ctx context.Context, dv *v1alpha1.DRBDVolume) error {
	isPrimary := dv.Spec.PrimaryNode == a.nodeID
	isSecondary := dv.Spec.SecondaryNode == a.nodeID

	if !isPrimary && !isSecondary {
		return nil
	}

	// Skip if being deleted
	if dv.Status.Phase == v1alpha1.VolumePhaseDeleting {
		return nil
	}

	// Skip if secondary not yet assigned
	if dv.Spec.SecondaryNode == "" {
		return nil
	}

	// Check if we already completed our setup
	if isPrimary && dv.Status.PrimaryReady {
		return nil
	}
	if isSecondary && dv.Status.SecondaryReady {
		return nil
	}

	log := a.log.With("drbdvolume", dv.Name, "role", roleString(isPrimary))

	// Get node IPs for DRBD config
	primaryIP, err := a.getNodeIP(ctx, dv.Spec.PrimaryNode)
	if err != nil {
		return fmt.Errorf("failed to get primary node IP: %w", err)
	}
	secondaryIP, err := a.getNodeIP(ctx, dv.Spec.SecondaryNode)
	if err != nil {
		return fmt.Errorf("failed to get secondary node IP: %w", err)
	}

	// Set up the local and remote config based on role
	var localNodeName, remoteNodeName, localAddr, remoteAddr string
	if isPrimary {
		localNodeName = dv.Spec.PrimaryNode
		remoteNodeName = dv.Spec.SecondaryNode
		localAddr = primaryIP
		remoteAddr = secondaryIP
	} else {
		localNodeName = dv.Spec.SecondaryNode
		remoteNodeName = dv.Spec.PrimaryNode
		localAddr = secondaryIP
		remoteAddr = primaryIP
	}

	// Secondary must create the LV first (primary already has it from CreateVolume)
	if isSecondary {
		if !lvm.LvExists(a.log, a.vgName, dv.Spec.VolumeName) {
			log.Info("creating lv on secondary node")
			output, err := lvm.CreateLV(a.log, a.vgName, dv.Spec.VolumeName, uint64(dv.Spec.SizeBytes), dv.Spec.LVMType, false)
			if err != nil {
				return fmt.Errorf("failed to create lv on secondary: %w, output: %s", err, output)
			}
		}
	}

	// Write DRBD resource config if not present
	if !drbd.ResourceExists(dv.Spec.VolumeName) {
		log.Info("writing drbd resource config")
		cfg := drbd.ResourceConfig{
			Name:           dv.Spec.VolumeName,
			Protocol:       dv.Spec.DRBDProtocol,
			Minor:          dv.Spec.DRBDMinor,
			Port:           dv.Spec.DRBDPort,
			VGName:         a.vgName,
			LocalNodeName:  localNodeName,
			LocalAddr:      localAddr,
			RemoteNodeName: remoteNodeName,
			RemoteAddr:     remoteAddr,
		}

		if err := drbd.WriteResourceConfig(a.log, cfg); err != nil {
			return fmt.Errorf("failed to write drbd resource config: %w", err)
		}

		// Create DRBD metadata
		if output, err := drbd.CreateMetadata(a.log, dv.Spec.VolumeName); err != nil {
			return fmt.Errorf("failed to create drbd metadata: %w, output: %s", err, output)
		}

		// Bring up DRBD resource
		if output, err := drbd.Up(a.log, dv.Spec.VolumeName); err != nil {
			return fmt.Errorf("failed to bring up drbd: %w, output: %s", err, output)
		}
	}

	// Mark this side as ready
	// Re-fetch to avoid conflicts
	var fresh v1alpha1.DRBDVolume
	if err := a.client.Get(ctx, types.NamespacedName{Name: dv.Name}, &fresh); err != nil {
		return fmt.Errorf("failed to re-fetch drbdvolume: %w", err)
	}

	updated := false
	if isPrimary && !fresh.Status.PrimaryReady {
		fresh.Status.PrimaryReady = true
		fresh.Status.Phase = v1alpha1.VolumePhasePrimaryReady
		updated = true
	}
	if isSecondary && !fresh.Status.SecondaryReady {
		fresh.Status.SecondaryReady = true
		fresh.Status.Phase = v1alpha1.VolumePhaseSecondaryReady
		updated = true
	}

	if updated {
		log.Info("marking node as ready for drbd volume")
		if err := a.client.Status().Update(ctx, &fresh); err != nil {
			return fmt.Errorf("failed to update drbdvolume status: %w", err)
		}
	}

	return nil
}

// TeardownDRBD tears down a DRBD resource and removes the LV on this node.
// Called during volume deletion.
func (a *Agent) TeardownDRBD(dv *v1alpha1.DRBDVolume) error {
	log := a.log.With("drbdvolume", dv.Name)

	if drbd.ResourceExists(dv.Spec.VolumeName) {
		log.Info("tearing down drbd resource")

		if _, err := drbd.Down(a.log, dv.Spec.VolumeName); err != nil {
			log.Error("failed to bring down drbd (may already be down)", "error", err)
		}

		if err := drbd.RemoveResourceConfig(a.log, dv.Spec.VolumeName); err != nil {
			log.Error("failed to remove drbd resource config", "error", err)
		}
	}

	return nil
}

// getNodeIP looks up the InternalIP of a node.
func (a *Agent) getNodeIP(ctx context.Context, nodeName string) (string, error) {
	var node corev1.Node
	if err := a.client.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
		return "", fmt.Errorf("failed to get node %s: %w", nodeName, err)
	}

	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			ip := net.ParseIP(addr.Address)
			if ip == nil {
				continue
			}
			return addr.Address, nil
		}
	}

	return "", fmt.Errorf("no InternalIP found for node %s", nodeName)
}

func roleString(isPrimary bool) string {
	if isPrimary {
		return "primary"
	}
	return "secondary"
}
