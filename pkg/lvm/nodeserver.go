/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package lvm

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/docker/go-units"
	"golang.org/x/net/context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
)

const topologyKeyNode = "topology.lvm.csi/node"

type nodeServer struct {
	nodeID            string
	ephemeral         bool
	maxVolumesPerNode int64
	devicesPattern    string
	vgName            string
}

func newNodeServer(nodeID string, ephemeral bool, maxVolumesPerNode int64, devicesPattern string, vgName string) *nodeServer {

	// revive existing volumes at start of node server
	vgexists := VgExists(vgName)
	if !vgexists {
		klog.Infof("volumegroup: %s not found\n", vgName)
		VgActivate(vgName)
		// now check again for existing vg again
		vgexists = VgExists(vgName)
		if !vgexists {
			klog.Infof("volumegroup: %s not found\n", vgName)
			return nil
		}
	}
	cmd := exec.Command("lvchange", "-ay", vgName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to activate logical volumes:%s %v", out, err)
	}

	return &nodeServer{
		nodeID:            nodeID,
		ephemeral:         ephemeral,
		maxVolumesPerNode: maxVolumesPerNode,
		devicesPattern:    devicesPattern,
		vgName:            vgName,
	}
}

func (ns *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {

	// Check arguments
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	targetPath := req.GetTargetPath()

	if req.GetVolumeCapability().GetBlock() != nil &&
		req.GetVolumeCapability().GetMount() != nil {
		return nil, status.Error(codes.InvalidArgument, "cannot have both block and mount access type")
	}

	var accessTypeMount, accessTypeBlock bool

	ephemeralVolume := req.GetVolumeContext()["csi.storage.k8s.io/ephemeral"] == "true" ||
		req.GetVolumeContext()["csi.storage.k8s.io/ephemeral"] == "" && ns.ephemeral // Kubernetes 1.15 doesn't have csi.storage.k8s.io/ephemeral.

	cap := req.GetVolumeCapability()

	if cap.GetBlock() != nil {
		accessTypeBlock = true
	}
	if cap.GetMount() != nil {
		accessTypeMount = true
	}

	// sanity checks (probably more sanity checks are needed later)
	if accessTypeBlock && accessTypeMount {
		return nil, status.Error(codes.InvalidArgument, "cannot have both block and mount access type")
	}

	// TODO
	// impelment ephemeral, either by creation of a provisioner pod (though not needed, since this runs on the node already)
	// or directly by a shared, exported func

	// if ephemeral is specified, create volume here to avoid errors
	if ephemeralVolume {
		capacityBytes, err := strconv.Atoi(req.GetVolumeContext()["RequiredBytes"])

		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create volume %v: %v", req.GetVolumeId(), err)
		}
		capacity := int64(capacityBytes)

		if capacity >= maxStorageCapacity {
			return nil, status.Errorf(codes.OutOfRange, "Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity)
		}

		lvmType := req.GetVolumeContext()["type"]
		if !(lvmType == "linear" || lvmType == "mirror" || lvmType == "striped") {
			return nil, status.Errorf(codes.Internal, "lvmType is incorrect: %2", lvmType)
		}

		var requestedAccessType accessType

		if accessTypeBlock {
			requestedAccessType = blockAccess
		} else {
			// Default to mount.
			requestedAccessType = mountAccess
		}

		volID := req.GetVolumeId()

		val := req.GetVolumeContext()["size"]
		klog.V(4).Infof("size: created volume: %s", val)
		if val == "" {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("ephemeral inline volume is missing size parameter"))
		}
		size, err := units.RAMInBytes(val)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to parse size(%s) of ephemeral inline volume: %s", val, err.Error()))
		}

		klog.V(4).Infof("TODO: ephemeral mode: created volume: %s, %s, %s", volID, size, requestedAccessType)

		klog.V(4).Infof("ephemeral mode: created volume: %s", volID)
	}

	if req.GetVolumeCapability().GetBlock() != nil {

		output, err := bindMountLV(req.GetVolumeId(), targetPath, ns.vgName)
		if err != nil {
			return nil, fmt.Errorf("unable to bind mount lv: %v output:%s", err, output)
		}
		klog.Infof("block lv %s size:%d vg:%s devices:%s created", req.GetVolumeId(), req.GetVolumeCapability(), ns.vgName, ns.devicesPattern, targetPath)

	} else if req.GetVolumeCapability().GetMount() != nil {

		output, err := mountLV(req.GetVolumeId(), targetPath, ns.vgName)
		if err != nil {
			return nil, fmt.Errorf("unable to mount lv: %v output:%s", err, output)
		}
		klog.Infof("mounted lv %s size:%d vg:%s devices:%s created", req.GetVolumeId(), req.VolumeCapability, ns.vgName, ns.devicesPattern, targetPath)

	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {

	// TODO
	// implement deletion of ephemeral volumes

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	output, err := umountLV(req.GetVolumeId(), ns.vgName)
	if err != nil {
		return nil, fmt.Errorf("unable to delete lv: %v output:%s", err, output)
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capability missing in request")
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (ns *nodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {

	topology := &csi.Topology{
		Segments: map[string]string{topologyKeyNode: ns.nodeID},
	}

	return &csi.NodeGetInfoResponse{
		NodeId:             ns.nodeID,
		MaxVolumesPerNode:  ns.maxVolumesPerNode,
		AccessibleTopology: topology,
	}, nil
}

func (ns *nodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
					},
				},
			},
		},
	}, nil
}

func (ns *nodeServer) NodeGetVolumeStats(ctx context.Context, in *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (ns *nodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
