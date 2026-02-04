package server

import (
	"context"
	"fmt"
	"strconv"
	"time"

	v1alpha1 "github.com/metal-stack/csi-driver-lvm/api/v1alpha1"
	"github.com/metal-stack/csi-driver-lvm/pkg/drbd"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// drbdMinorCounter is a simple in-memory counter for assigning DRBD minor numbers.
// In production, the DRBDReplicationController should manage this via the CRD.
var (
	drbdMinorCounter = 100
	drbdPortCounter  = 7900
)

func (d *Driver) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	// Check arguments
	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume name missing in request")
	}
	caps := req.GetVolumeCapabilities()
	if caps == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities missing in request")
	}

	var (
		// Keep a record of the requested access types.
		accessTypeMount, accessTypeBlock bool

		integrity = false
	)

	for _, cap := range caps {
		if cap.GetBlock() != nil {
			accessTypeBlock = true
		}
		if cap.GetMount() != nil {
			accessTypeMount = true
		}
	}

	if accessTypeBlock && accessTypeMount {
		return nil, status.Error(codes.InvalidArgument, "cannot have both block and mount access type")
	}

	lvmType := req.GetParameters()["type"]
	switch lvmType {
	case "linear", "mirror", "striped":
		// these are supported lvm types
	default:
		return nil, status.Errorf(codes.Internal, "lvmType is incorrect: %s", lvmType)
	}

	if value, ok := req.GetParameters()["integrity"]; ok {
		var err error
		integrity, err = strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("unable to parse integrity parameter to bool: %w", err)
		}
	}

	replication := req.GetParameters()["replication"]

	d.log.Info("creating volume", "name", req.GetName(), "replication", replication)

	requiredBytes := req.GetCapacityRange().GetRequiredBytes()

	_, err := lvm.CreateLV(d.log, d.vgName, req.GetName(), uint64(requiredBytes), lvmType, integrity)
	if err != nil {
		return nil, fmt.Errorf("unable to create lv %s: %w", req.GetName(), err)
	}

	d.log.Info("successfully created lv", "name", req.GetName())

	volumeContext := req.GetParameters()
	volumeContext["RequiredBytes"] = strconv.FormatInt(requiredBytes, 10)

	topology := []*csi.Topology{{
		Segments: map[string]string{topologyKeyNode: d.nodeId},
	}}

	// Handle DRBD replication
	if replication == "drbd" && d.k8sClient != nil {
		dv, err := d.createDRBDVolume(ctx, req.GetName(), requiredBytes, lvmType, req.GetParameters())
		if err != nil {
			return nil, fmt.Errorf("unable to create drbd volume: %w", err)
		}

		// Wait for the secondary to be assigned and DRBD to be set up
		if err := d.waitForDRBDReady(ctx, dv.Name); err != nil {
			d.log.Warn("drbd not fully ready yet, volume will be available once replication establishes", "error", err)
		}

		// If secondary was assigned, include it in the topology
		var fresh v1alpha1.DRBDVolume
		if err := d.k8sClient.Get(ctx, types.NamespacedName{Name: dv.Name}, &fresh); err == nil && fresh.Spec.SecondaryNode != "" {
			topology = append(topology, &csi.Topology{
				Segments: map[string]string{topologyKeyNode: fresh.Spec.SecondaryNode},
			})
		}

		volumeContext["drbdVolume"] = dv.Name
		volumeContext["drbdMinor"] = strconv.Itoa(dv.Spec.DRBDMinor)
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           req.GetName(),
			CapacityBytes:      requiredBytes,
			VolumeContext:      volumeContext,
			ContentSource:      req.GetVolumeContentSource(),
			AccessibleTopology: topology,
		},
	}, nil
}

func (d *Driver) createDRBDVolume(ctx context.Context, volumeName string, sizeBytes int64, lvmType string, params map[string]string) (*v1alpha1.DRBDVolume, error) {
	// Check if already exists
	var existing v1alpha1.DRBDVolume
	err := d.k8sClient.Get(ctx, types.NamespacedName{Name: volumeName}, &existing)
	if err == nil {
		return &existing, nil
	}

	protocol := params["drbdProtocol"]
	if protocol == "" {
		protocol = "C"
	}

	d.Lock()
	minor := drbdMinorCounter
	drbdMinorCounter++
	port := drbdPortCounter
	drbdPortCounter++
	d.Unlock()

	dv := &v1alpha1.DRBDVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: volumeName,
		},
		Spec: v1alpha1.DRBDVolumeSpec{
			VolumeName:   volumeName,
			SizeBytes:    sizeBytes,
			PrimaryNode:  d.nodeId,
			VGName:       d.vgName,
			LVMType:      lvmType,
			DRBDMinor:    minor,
			DRBDPort:     port,
			DRBDProtocol: protocol,
		},
	}

	if err := d.k8sClient.Create(ctx, dv); err != nil {
		return nil, fmt.Errorf("failed to create DRBDVolume CR: %w", err)
	}

	dv.Status.Phase = v1alpha1.VolumePhasePending
	if err := d.k8sClient.Status().Update(ctx, dv); err != nil {
		d.log.Warn("failed to set initial drbd volume status", "error", err)
	}

	d.log.Info("created DRBDVolume CR", "name", volumeName, "minor", minor, "port", port)
	return dv, nil
}

func (d *Driver) waitForDRBDReady(ctx context.Context, dvName string) error {
	timeout := 60 * time.Second
	pollInterval := 2 * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		var dv v1alpha1.DRBDVolume
		if err := d.k8sClient.Get(ctx, types.NamespacedName{Name: dvName}, &dv); err != nil {
			return err
		}

		if dv.Status.Phase == v1alpha1.VolumePhaseEstablished {
			return nil
		}

		if dv.Status.PrimaryReady && dv.Status.SecondaryReady {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("timed out waiting for drbd volume %s to become ready", dvName)
}

func (d *Driver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id missing in request")
	}

	d.log.Info("trying to delete volume", "volume-id", req.VolumeId)

	// Tear down DRBD if this is a replicated volume
	if d.k8sClient != nil {
		var dv v1alpha1.DRBDVolume
		err := d.k8sClient.Get(ctx, types.NamespacedName{Name: req.VolumeId}, &dv)
		if err == nil {
			d.log.Info("tearing down drbd for volume", "volume-id", req.VolumeId)

			// Mark as deleting
			dv.Status.Phase = v1alpha1.VolumePhaseDeleting
			if err := d.k8sClient.Status().Update(ctx, &dv); err != nil {
				d.log.Warn("failed to update drbd volume phase to deleting", "error", err)
			}

			// Tear down local DRBD
			if drbd.ResourceExists(req.VolumeId) {
				if _, err := drbd.Down(d.log, req.VolumeId); err != nil {
					d.log.Warn("failed to bring down drbd", "error", err)
				}
				if err := drbd.RemoveResourceConfig(d.log, req.VolumeId); err != nil {
					d.log.Warn("failed to remove drbd config", "error", err)
				}
			}

			// Delete the DRBDVolume CR (the secondary node agent will clean up its side)
			if err := d.k8sClient.Delete(ctx, &dv); client.IgnoreNotFound(err) != nil {
				d.log.Warn("failed to delete DRBDVolume CR", "error", err)
			}
		}
	}

	existsVolume := lvm.LvExists(d.log, d.vgName, req.VolumeId)
	if !existsVolume {
		return &csi.DeleteVolumeResponse{}, nil
	}

	_, err := lvm.RemoveLVS(d.log, d.vgName, req.VolumeId)
	if err != nil {
		return nil, fmt.Errorf("unable to delete volume with id %s: %w", req.VolumeId, err)
	}

	d.log.Info("volume successfully deleted", "volume-id", req.VolumeId)

	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					},
				},
			},
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_GET_CAPACITY,
					},
				},
			},
		},
	}, nil
}

func (d *Driver) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id cannot be empty")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities cannot be empty")
	}

	for _, cap := range req.GetVolumeCapabilities() {
		if cap.GetMount() == nil && cap.GetBlock() == nil {
			return nil, status.Error(codes.InvalidArgument, "cannot have both mount and block access type be undefined")
		}

		// A real driver would check the capabilities of the given volume with
		// the set of requested capabilities.
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.GetVolumeContext(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	}, nil
}

func (d *Driver) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	lvmType := req.GetParameters()["type"]

	switch lvmType {
	case "linear", "mirror", "striped":
		// These are supported lvm types
	default:
		return nil, status.Errorf(codes.Internal, "lvmType is incorrect: %s", lvmType)
	}

	totalBytes, err := lvm.VgStats(d.log, d.vgName)
	if err != nil {
		return nil, fmt.Errorf("unable to get capacity of vg %s", d.vgName)
	}

	// adjust available capacity for mirrored volumes
	// as we only offer a single mirror we do not need something more specific for calculation
	if lvmType == "mirror" {
		totalBytes = totalBytes / 2
	}

	d.log.Debug("available capacity", "bytes", totalBytes, "lvm-type", lvmType)

	return &csi.GetCapacityResponse{
		AvailableCapacity: totalBytes,
		MaximumVolumeSize: wrapperspb.Int64(totalBytes),
		MinimumVolumeSize: wrapperspb.Int64(0),
	}, nil
}
