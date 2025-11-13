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
	"k8s.io/klog/v2"
)

func (d *Driver) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	nodeName := req.GetAccessibilityRequirements().GetPreferred()[0].GetSegments()[topologyKeyNode]
	if !d.isRequestForThisNode(nodeName) {
		//skip gracefully?
		return &csi.CreateVolumeResponse{}, nil
	}

	// Check arguments
	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}
	caps := req.GetVolumeCapabilities()
	if caps == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
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
		// These are supported lvm types
	default:
		return nil, status.Errorf(codes.Internal, "lvmType is incorrect: %s", lvmType)
	}

	integrity, err := strconv.ParseBool(req.GetParameters()["integrity"])
	if err != nil {
		klog.Warningf("Could not parse 'integrity' request parameter, assuming false: %s", err)
	}

	volumeContext := req.GetParameters()
	size := strconv.FormatInt(req.GetCapacityRange().GetRequiredBytes(), 10)

	volumeContext["RequiredBytes"] = size

	klog.Infof("creating volume %s on node: %s", req.GetName(), nodeName)

	_, err = lvm.CreateLVS(d.vgName, req.GetName(), uint64(req.GetCapacityRange().GetRequiredBytes()), lvmType, integrity)
	if err != nil {
		return nil, fmt.Errorf("unable to create lv  %s: %w", req.GetName(), err)
	}

	klog.Infof("successfully created lv %s", req.GetName())

	topology := []*csi.Topology{{
		Segments: map[string]string{topologyKeyNode: nodeName},
	}}
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           req.GetName(),
			CapacityBytes:      req.GetCapacityRange().GetRequiredBytes(),
			VolumeContext:      volumeContext,
			ContentSource:      req.GetVolumeContentSource(),
			AccessibleTopology: topology,
		},
	}, nil
}

func (d *Driver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	existsVolume := lvm.LvExists(d.vgName, req.VolumeId)
	if !existsVolume {
		return &csi.DeleteVolumeResponse{}, nil
	}

	klog.Infof("getting capacity for node: %s and volume: %s", d.nodeId, req.VolumeId)

	_, err := lvm.RemoveLVS(d.vgName, req.VolumeId)
	if err != nil {
		return nil, fmt.Errorf("unable to delete volume with id %s: %w", req.VolumeId, err)
	}
	klog.V(4).Infof("volume %v successfully deleted", req.VolumeId)

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
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, req.GetVolumeId())
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
	nodeName := req.GetAccessibleTopology().GetSegments()[topologyKeyNode]

	if !d.isRequestForThisNode(nodeName) {
		return &csi.GetCapacityResponse{}, nil
	}

	lvmType := req.GetParameters()["type"]
	switch lvmType {
	case "linear", "mirror", "striped":
		// These are supported lvm types
	default:
		return nil, status.Errorf(codes.Internal, "lvmType is incorrect: %s", lvmType)
	}

	klog.Infof("getting capacity for node: %s and lvm-type: %s", nodeName, lvmType)

	totalBytes, err := lvm.VgStats(d.vgName)
	if err != nil {
		return nil, fmt.Errorf("unable to get capacity of vg %s", d.vgName)
	}

	return &csi.GetCapacityResponse{
		AvailableCapacity: totalBytes,
		MaximumVolumeSize: wrapperspb.Int64(totalBytes),
		MinimumVolumeSize: wrapperspb.Int64(0),
	}, nil

}

func (d *Driver) isRequestForThisNode(nodeName string) bool {
	return d.nodeId == nodeName
}
