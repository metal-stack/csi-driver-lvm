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
	"google.golang.org/protobuf/types/known/wrapperspb"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v1 "k8s.io/api/core/v1"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

type controllerServer struct {
	caps             []*csi.ControllerServiceCapability
	nodeID           string
	devicesPattern   string
	vgName           string
	hostWritePath    string
	kubeClient       kubernetes.Clientset
	provisionerImage string
	pullPolicy       v1.PullPolicy
	namespace        string
}

// NewControllerServer
func newControllerServer(ephemeral bool, nodeID string, devicesPattern string, vgName string, hostWritePath string, namespace string, provisionerImage string, pullPolicy v1.PullPolicy) (*controllerServer, error) {
	if ephemeral {
		return &controllerServer{caps: getControllerServiceCapabilities(nil), nodeID: nodeID}, nil
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	// creates the clientset
	kubeClient, err := kubernetes.NewForConfig(config)

	if err != nil {
		return nil, err
	}
	return &controllerServer{
		caps: getControllerServiceCapabilities(
			[]csi.ControllerServiceCapability_RPC_Type{
				csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
				csi.ControllerServiceCapability_RPC_GET_CAPACITY,
				// TODO
				//				csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
				//				csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
				//				csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
				//				csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
			}),
		nodeID:           nodeID,
		devicesPattern:   devicesPattern,
		hostWritePath:    hostWritePath,
		vgName:           vgName,
		kubeClient:       *kubeClient,
		namespace:        namespace,
		provisionerImage: provisionerImage,
		pullPolicy:       pullPolicy,
	}, nil
}

func (cs *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		klog.V(3).Infof("invalid create volume req: %v", req)
		return nil, err
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
	if !(lvmType == "linear" || lvmType == "mirror" || lvmType == "striped") {
		return nil, status.Errorf(codes.Internal, "lvmType is incorrect: %s", lvmType)
	}

	volumeContext := req.GetParameters()
	size := strconv.FormatInt(req.GetCapacityRange().GetRequiredBytes(), 10)

	volumeContext["RequiredBytes"] = size

	// schedulded node of the pod is the first entry in the preferred segment
	node := req.GetAccessibilityRequirements().GetPreferred()[0].GetSegments()[topologyKeyNode]
	topology := []*csi.Topology{{
		Segments: map[string]string{topologyKeyNode: node},
	}}
	klog.Infof("creating volume %s on node: %s", req.GetName(), node)

	va := volumeAction{
		action:           actionTypeCreate,
		name:             req.GetName(),
		nodeName:         node,
		size:             req.GetCapacityRange().GetRequiredBytes(),
		lvmType:          lvmType,
		devicesPattern:   cs.devicesPattern,
		pullPolicy:       cs.pullPolicy,
		provisionerImage: cs.provisionerImage,
		kubeClient:       cs.kubeClient,
		namespace:        cs.namespace,
		vgName:           cs.vgName,
		hostWritePath:    cs.hostWritePath,
	}
	if err := createProvisionerPod(ctx, va); err != nil {
		klog.Errorf("error creating provisioner pod :%v", err)
		return nil, err
	}

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

func (cs *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		klog.V(3).Infof("invalid delete volume req: %v", req)
		return nil, err
	}

	volID := req.GetVolumeId()

	volume, err := cs.kubeClient.CoreV1().PersistentVolumes().Get(ctx, volID, metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	}
	klog.V(4).Infof("volume %s to be deleted", volume)
	ns := volume.Spec.NodeAffinity.Required.NodeSelectorTerms
	node := ns[0].MatchExpressions[0].Values[0]

	klog.V(4).Infof("from node %s ", node)

	_, err = cs.kubeClient.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
	if err != nil {
		if k8serror.IsNotFound(err) {
			klog.Infof("node %s not found anymore. Assuming volume %s is gone for good.", node, volID)
			return &csi.DeleteVolumeResponse{}, nil
		} else {
			klog.Errorf("error getting nodes: %v", err)
			return nil, err
		}
	}

	va := volumeAction{
		action:           actionTypeDelete,
		name:             req.GetVolumeId(),
		nodeName:         node,
		pullPolicy:       cs.pullPolicy,
		provisionerImage: cs.provisionerImage,
		kubeClient:       cs.kubeClient,
		namespace:        cs.namespace,
		vgName:           cs.vgName,
		hostWritePath:    cs.hostWritePath,
	}
	if err := createProvisionerPod(ctx, va); err != nil {
		klog.Errorf("error creating provisioner pod :%v", err)
		return nil, err
	}

	klog.V(4).Infof("volume %v successfully deleted", volID)

	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *controllerServer) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	nodeName := req.GetAccessibleTopology().GetSegments()[topologyKeyNode]
	lvmType := req.GetParameters()["type"]
	totalBytes, err := createGetCapacityPod(ctx, volumeAction{
		action:           "getcapacity",
		name:             fmt.Sprintf("%s-%s", lvmType, nodeName),
		nodeName:         nodeName,
		lvmType:          lvmType,
		devicesPattern:   cs.devicesPattern,
		provisionerImage: cs.provisionerImage,
		pullPolicy:       cs.pullPolicy,
		kubeClient:       cs.kubeClient,
		namespace:        cs.namespace,
		vgName:           cs.vgName,
		hostWritePath:    cs.hostWritePath,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to query devices capacity: %v", err)
	}

	return &csi.GetCapacityResponse{
		AvailableCapacity: totalBytes,
		MaximumVolumeSize: wrapperspb.Int64(totalBytes),
		MinimumVolumeSize: wrapperspb.Int64(0),
	}, nil
}

func (cs *controllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: cs.caps,
	}, nil
}

func (cs *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if len(req.VolumeCapabilities) == 0 {
		return nil, status.Error(codes.InvalidArgument, req.VolumeId)
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

func (cs *controllerServer) validateControllerServiceRequest(c csi.ControllerServiceCapability_RPC_Type) error {
	if c == csi.ControllerServiceCapability_RPC_UNKNOWN {
		return nil
	}

	for _, cap := range cs.caps {
		if c == cap.GetRpc().GetType() {
			return nil
		}
	}
	return status.Errorf(codes.InvalidArgument, "unsupported capability %s", c)
}

func getControllerServiceCapabilities(cl []csi.ControllerServiceCapability_RPC_Type) []*csi.ControllerServiceCapability {
	var csc []*csi.ControllerServiceCapability

	for _, cap := range cl {
		klog.Infof("Enabling controller service capability: %v", cap.String())
		csc = append(csc, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		})
	}

	return csc
}

// Following functions will never be implemented
// use the "NodeXXX" versions of the nodeserver instead

func (cs *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
