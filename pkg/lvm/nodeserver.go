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
	"os"

	"github.com/golang/glog"
	"golang.org/x/net/context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
)

const TopologyKeyNode = "topology.lvm.csi/node"

type nodeServer struct {
	nodeID            string
	ephemeral         bool
	maxVolumesPerNode int64
	devicesPattern    string
	vgName            string
}

func NewNodeServer(nodeId string, ephemeral bool, maxVolumesPerNode int64, devicesPattern string, vgName string) *nodeServer {
	return &nodeServer{
		nodeID:            nodeId,
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
	ephemeralVolume := req.GetVolumeContext()["csi.storage.k8s.io/ephemeral"] == "true" ||
		req.GetVolumeContext()["csi.storage.k8s.io/ephemeral"] == "" && ns.ephemeral // Kubernetes 1.15 doesn't have csi.storage.k8s.io/ephemeral.

	if req.GetVolumeCapability().GetBlock() != nil &&
		req.GetVolumeCapability().GetMount() != nil {
		return nil, status.Error(codes.InvalidArgument, "cannot have both block and mount access type")
	}

	// TODO
	// lvmType is currently hard-coded to linear
	// https://github.com/kubernetes-csi/external-provisioner/pull/399
	lvmType := "linear"

	// if ephemeral is specified, create volume here to avoid errors
	if ephemeralVolume {
		volID := req.GetVolumeId()
		volName := fmt.Sprintf("ephemeral-%s", volID)
		err := createLvmVolume(req.GetVolumeId(), volName, maxStorageCapacity, mountAccess, ephemeralVolume, ns.devicesPattern, lvmType, ns.vgName)
		if err != nil && !os.IsExist(err) {
			glog.Error("ephemeral mode failed to create volume: ", err)
			return nil, status.Error(codes.Internal, err.Error())
		}
		glog.V(4).Infof("ephemeral mode: created volume: %s", volID)
	}

	if req.GetVolumeCapability().GetBlock() != nil {

		output, err := bindMountLV(req.GetVolumeId(), targetPath, ns.vgName)
		if err != nil {
			return nil, fmt.Errorf("unable to bind mount lv: %v output:%s", err, output)
		}
		klog.Infof("block lv %s size:%d vg:%s devices:%s created", req.GetVolumeId(), req.GetVolumeCapability(), ns.vgName, ns.devicesPattern, lvmType, targetPath)

	} else if req.GetVolumeCapability().GetMount() != nil {

		output, err := mountLV(req.GetVolumeId(), targetPath, ns.vgName)
		if err != nil {
			return nil, fmt.Errorf("unable to mount lv: %v output:%s", err, output)
		}
		klog.Infof("mounted lv %s size:%d vg:%s devices:%s created", req.GetVolumeId(), req.VolumeCapability, ns.vgName, ns.devicesPattern, lvmType, targetPath)

	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {

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
		Segments: map[string]string{TopologyKeyNode: ns.nodeID},
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

// NodeExpandVolume is only implemented so the driver can be used for e2e testing.
func (ns *nodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
