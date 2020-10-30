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
	"os/exec"
	"strings"

	"context"

	"github.com/docker/go-units"
	"golang.org/x/sys/unix"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

const topologyKeyNode = "topology.lvm.csi/node"

type nodeServer struct {
	nodeID            string
	ephemeral         bool
	devicesPattern    string
	vgName            string
}

func newNodeServer(nodeID string, ephemeral bool,  devicesPattern string, vgName string) *nodeServer {

	// revive existing volumes at start of node server
	vgexists := vgExists(vgName)
	if !vgexists {
		klog.Infof("volumegroup: %s not found\n", vgName)
		vgActivate(vgName)
		// now check again for existing vg again
	}
	cmd := exec.Command("lvchange", "-ay", vgName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to activate logical volumes:%s %v", out, err)
	}

	return &nodeServer{
		nodeID:            nodeID,
		ephemeral:         ephemeral,
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

	ephemeralVolume := req.GetVolumeContext()["csi.storage.k8s.io/ephemeral"] == "true" ||
		req.GetVolumeContext()["csi.storage.k8s.io/ephemeral"] == "" && ns.ephemeral // Kubernetes 1.15 doesn't have csi.storage.k8s.io/ephemeral.

	// if ephemeral is specified, create volume here
	if ephemeralVolume {

		val := req.GetVolumeContext()["size"]
		if val == "" {
			return nil, status.Error(codes.InvalidArgument, "ephemeral inline volume is missing size parameter")
		}
		size, err := units.RAMInBytes(val)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to parse size(%s) of ephemeral inline volume: %s", val, err.Error()))
		}

		volID := req.GetVolumeId()

		output, err := CreateVG(ns.vgName, ns.devicesPattern)
		if err != nil {
			return nil, fmt.Errorf("unable to create vg: %v output:%s", err, output)
		}

		output, err = CreateLVS(context.Background(), ns.vgName, volID, uint64(size), req.GetVolumeContext()["type"])
		if err != nil {
			return nil, fmt.Errorf("unable to create lv: %v output:%s", err, output)
		}

		klog.V(4).Infof("ephemeral mode: created volume: %s, size: %d", volID, size)
	}

	if req.GetVolumeCapability().GetBlock() != nil {

		output, err := bindMountLV(req.GetVolumeId(), targetPath, ns.vgName)
		if err != nil {
			return nil, fmt.Errorf("unable to bind mount lv: %v output:%s", err, output)
		}
		// FIXME: VolumeCapability is a struct and not the size
		klog.Infof("block lv %s size:%s vg:%s devices:%s created at:%s", req.GetVolumeId(), req.GetVolumeCapability(), ns.vgName, ns.devicesPattern, targetPath)

	} else if req.GetVolumeCapability().GetMount() != nil {

		output, err := mountLV(req.GetVolumeId(), targetPath, ns.vgName)
		if err != nil {
			return nil, fmt.Errorf("unable to mount lv: %v output:%s", err, output)
		}
		// FIXME: VolumeCapability is a struct and not the size
		klog.Infof("mounted lv %s size:%s vg:%s devices:%s created at:%s", req.GetVolumeId(), req.GetVolumeCapability(), ns.vgName, ns.devicesPattern, targetPath)

	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {

	// TODO
	// implement deletion of ephemeral volumes
	volID := req.GetVolumeId()

	klog.Infof("NodeUnpublishRequest: %s", req)
	// Check arguments
	if len(volID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	output, err := umountLV(req.GetTargetPath())
	if err != nil {
		return nil, fmt.Errorf("unable to umount lv: %v output:%s", err, output)
	}

	// ephemeral volumes start with "csi-"
	if strings.HasPrefix(volID, "csi-") {
		// remove ephemeral volume here
		output, err := RemoveLVS(context.Background(), ns.vgName, volID)
		if err != nil {
			return nil, fmt.Errorf("unable to delete lv: %v output:%s", err, output)
		}
		klog.Infof("lv %s vg:%s deleted", volID, ns.vgName)

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
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
					},
				},
			},
		},
	}, nil
}

func (ns *nodeServer) NodeGetVolumeStats(ctx context.Context, in *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {

	var fs unix.Statfs_t

	err := unix.Statfs(in.GetVolumePath(), &fs)
	if err != nil {
		return nil, err
	}

	diskFree := int64(fs.Bfree) * fs.Bsize
	diskTotal := int64(fs.Blocks) * fs.Bsize

	inodesFree := int64(fs.Ffree)
	inodesTotal := int64(fs.Files)

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Available: diskFree,
				Total:     diskTotal,
				Used:      diskTotal - diskFree,
				Unit:      csi.VolumeUsage_BYTES,
			},
			{
				Available: inodesFree,
				Total:     inodesTotal,
				Used:      inodesTotal - inodesFree,
				Unit:      csi.VolumeUsage_INODES,
			},
		},
	}, nil
}

func (ns *nodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {

	// Check arguments
	if req.GetCapacityRange() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	capacity := int64(req.GetCapacityRange().GetRequiredBytes())
	if capacity >= maxStorageCapacity {
		return nil, status.Errorf(codes.OutOfRange, "Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity)
	}

	volID := req.GetVolumeId()
	volPath := req.GetVolumePath()
	if len(volPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume path not provided")
	}

	info, err := os.Stat(volPath)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Could not get file information from %s: %v", volPath, err)
	}

	isBlock := false
	m := info.Mode()
	if !m.IsDir() {
		klog.Warning("volume expand request on block device: filesystem resize has to be done externally")
		isBlock = true
	}

	output, err := extendLVS(context.Background(), ns.vgName, volID, uint64(capacity), isBlock)

	if err != nil {
		return nil, fmt.Errorf("unable to umount lv: %v output:%s", err, output)

	}

	return &csi.NodeExpandVolumeResponse{
		CapacityBytes: capacity,
	}, nil

}
