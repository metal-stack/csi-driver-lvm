package server

import (
	"context"
	"fmt"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
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

	// Keep a record of the requested access types.
	var accessTypeMount, accessTypeBlock bool

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

	integrity, err := strconv.ParseBool(req.GetParameters()["integrity"])
	if err != nil {
		d.log.Warn("could not parse 'integrity' request parameter, assuming false", "error", err)
	}

	d.log.Info("creating volume", "name", req.GetName())

	requiredBytes := req.GetCapacityRange().GetRequiredBytes()

	_, err = lvm.CreateLV(d.log, d.vgName, req.GetName(), uint64(requiredBytes), lvmType, integrity)
	if err != nil {
		return nil, fmt.Errorf("unable to create lv %s: %w", req.GetName(), err)
	}

	d.log.Info("successfully created lv", "name", req.GetName())

	volumeContext := req.GetParameters()
	volumeContext["RequiredBytes"] = strconv.FormatInt(requiredBytes, 10)

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      req.GetName(),
			CapacityBytes: requiredBytes,
			VolumeContext: volumeContext,
			ContentSource: req.GetVolumeContentSource(),
			AccessibleTopology: []*csi.Topology{{
				Segments: map[string]string{topologyKeyNode: d.nodeId},
			}},
		},
	}, nil
}

func (d *Driver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id missing in request")
	}

	existsVolume := lvm.LvExists(d.log, d.vgName, req.VolumeId)
	if !existsVolume {
		return &csi.DeleteVolumeResponse{}, nil
	}

	d.log.Info("trying to delete volume", "volume-id", req.VolumeId)

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

	d.log.Info("available capacity", "bytes", totalBytes, "lvm-type", lvmType)

	return &csi.GetCapacityResponse{
		AvailableCapacity: totalBytes,
		MaximumVolumeSize: wrapperspb.Int64(totalBytes),
		MinimumVolumeSize: wrapperspb.Int64(0),
	}, nil
}
