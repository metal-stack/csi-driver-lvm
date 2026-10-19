package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const volumeContextRequiredBytes = "RequiredBytes"

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

	volumeMode := lvm.VolumeModeFilesystem
	if accessTypeBlock {
		volumeMode = lvm.VolumeModeBlock
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

	requiredBytes := req.GetCapacityRange().GetRequiredBytes()

	if req.GetVolumeContentSource().GetVolume() != nil {
		return nil, status.Error(codes.InvalidArgument, "cloning volumes is not supported")
	}
	if req.GetVolumeContentSource().GetSnapshot() != nil {
		return d.restoreVolume(ctx, restoreParams{
			Req:           req,
			LVMType:       lvmType,
			Integrity:     integrity,
			RequiredBytes: requiredBytes,
			VolumeMode:    volumeMode,
		})
	}

	d.log.Info("creating volume", "name", req.GetName())

	output, err := lvm.CreateLV(d.log, lvm.CreateLVParams{
		VG:        d.vgName,
		Name:      req.GetName(),
		Size:      uint64(requiredBytes), //nolint:gosec
		Type:      lvmType,
		Integrity: integrity,
		Tags:      []string{lvm.Tag(lvm.TagVolumeMode, volumeMode)},
	})
	if err != nil && lvm.IsInsufficientSpace(output) {
		return nil, status.Errorf(codes.ResourceExhausted, "not enough space in vg %s for volume %s: %s", d.vgName, req.GetName(), output)
	}
	if err != nil {
		return nil, fmt.Errorf("unable to create lv %s: %w (%s)", req.GetName(), err, output)
	}

	d.log.Info("successfully created lv", "name", req.GetName())

	return d.createVolumeResponse(req, requiredBytes), nil
}

func (d *Driver) createVolumeResponse(req *csi.CreateVolumeRequest, capacityBytes int64) *csi.CreateVolumeResponse {
	volumeContext := req.GetParameters()
	if volumeContext == nil {
		volumeContext = map[string]string{}
	}
	volumeContext[volumeContextRequiredBytes] = strconv.FormatInt(capacityBytes, 10)

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      req.GetName(),
			CapacityBytes: capacityBytes,
			VolumeContext: volumeContext,
			ContentSource: req.GetVolumeContentSource(),
			AccessibleTopology: []*csi.Topology{{
				Segments: map[string]string{topologyKeyNode: d.nodeId},
			}},
		},
	}
}

func (d *Driver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id missing in request")
	}

	release, err := d.volumeLocks.acquire(req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	defer release()

	existsVolume := lvm.LvExists(d.log, d.vgName, req.VolumeId)
	if !existsVolume {
		return &csi.DeleteVolumeResponse{}, nil
	}

	if d.restores.isTargetInUse(req.GetVolumeId()) {
		return nil, status.Errorf(codes.Aborted, "volume %s is still being restored from a snapshot", req.GetVolumeId())
	}

	// Removing a thick origin also removes its snapshots, which would silently break VolumeSnapshots.
	if err := d.ensureNoSnapshots(req.GetVolumeId(), codes.FailedPrecondition); err != nil {
		return nil, err
	}

	d.log.Info("trying to delete volume", "volume-id", req.VolumeId)

	_, err = lvm.RemoveLVS(d.log, d.vgName, req.VolumeId)
	if err != nil {
		return nil, fmt.Errorf("unable to delete volume with id %s: %w", req.VolumeId, err)
	}

	d.log.Info("volume successfully deleted", "volume-id", req.VolumeId)

	return &csi.DeleteVolumeResponse{}, nil
}

// ensureNoSnapshots fails with the given code if the volume is the origin of any snapshot.
func (d *Driver) ensureNoSnapshots(volumeID string, code codes.Code) error {
	snapshots, err := lvm.SnapshotsOf(d.log, d.vgName, volumeID)
	if err != nil {
		return status.Errorf(codes.Internal, "unable to list snapshots of volume %s: %v", volumeID, err)
	}
	if len(snapshots) == 0 {
		return nil
	}

	ids := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		ids = append(ids, snapshotID{Node: d.nodeId, VG: d.vgName, LV: snapshot.Name}.String())
	}
	return status.Errorf(code, "volume %s still has snapshots: %s", volumeID, strings.Join(ids, ", "))
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
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
					},
				},
			},
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
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
