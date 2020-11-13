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
	"math"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	v1 "k8s.io/api/core/v1"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	maxStorageCapacity = tib
)

type controllerServer struct {
	caps                        []*csi.ControllerServiceCapability
	nodeID                      string
	devicesPattern              string
	vgName                      string
	kubeClient                  kubernetes.Clientset
	provisionerImage            string
	pullPolicy                  v1.PullPolicy
	namespace                   string
	lvmTimeout                  int
	snapshotTimeout             int
	lvmSnapshotBufferPercentage int
}

// NewControllerServer
func newControllerServer(ephemeral bool, nodeID string, devicesPattern string, vgName string, namespace string, provisionerImage string, pullPolicy v1.PullPolicy, lvmTimeout int, snapshotTimeout int, lvmSnapshotBufferPercentage int) *controllerServer {
	if ephemeral {
		return &controllerServer{caps: getControllerServiceCapabilities(nil), nodeID: nodeID}
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	return &controllerServer{
		caps: getControllerServiceCapabilities(
			[]csi.ControllerServiceCapability_RPC_Type{
				csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
				csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
				csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,

				// TODO
				//				csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
				//				csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
			}),
		nodeID:                      nodeID,
		devicesPattern:              devicesPattern,
		vgName:                      vgName,
		kubeClient:                  *kubeClient,
		namespace:                   namespace,
		provisionerImage:            provisionerImage,
		pullPolicy:                  pullPolicy,
		lvmTimeout:                  lvmTimeout,
		snapshotTimeout:             snapshotTimeout,
		lvmSnapshotBufferPercentage: lvmSnapshotBufferPercentage,
	}
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

	// TODO
	// this check must bei implemented in createlvs executed by the provisioner pod on the node

	// Check for maximum available capacity
	capacity := int64(req.GetCapacityRange().GetRequiredBytes())
	if capacity >= maxStorageCapacity {
		return nil, status.Errorf(codes.OutOfRange, "Requested capacity %d exceeds maximum allowed %d", capacity, maxStorageCapacity)
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
		action:                      actionTypeCreate,
		name:                        req.GetName(),
		nodeName:                    node,
		size:                        req.GetCapacityRange().GetRequiredBytes(),
		lvmType:                     lvmType,
		devicesPattern:              cs.devicesPattern,
		pullPolicy:                  cs.pullPolicy,
		provisionerImage:            cs.provisionerImage,
		kubeClient:                  cs.kubeClient,
		namespace:                   cs.namespace,
		vgName:                      cs.vgName,
		lvmSnapshotBufferPercentage: cs.lvmSnapshotBufferPercentage,
	}
	if err := createProvisionerPod(va, cs.lvmTimeout); err != nil {
		klog.Errorf("error creating provisioner pod :%v", err)
		return nil, err
	}

	if req.GetVolumeContentSource() != nil {
		volumeSource := req.VolumeContentSource
		switch volumeSource.Type.(type) {
		case *csi.VolumeContentSource_Snapshot:
			if snapshot := volumeSource.GetSnapshot(); snapshot != nil {
				s3, err := secretsToS3Parameter(req.Secrets)
				klog.Infof("restore secrets: %v", req)
				if err != nil {
					return nil, err
				}

				va := volumeAction{
					action:                      actionTypeRestoreSnapshot,
					name:                        req.GetName(),
					nodeName:                    node,
					pullPolicy:                  cs.pullPolicy,
					provisionerImage:            cs.provisionerImage,
					kubeClient:                  cs.kubeClient,
					namespace:                   cs.namespace,
					vgName:                      cs.vgName,
					snapshotName:                snapshot.GetSnapshotId(),
					S3Parameter:                 s3,
					lvmSnapshotBufferPercentage: cs.lvmSnapshotBufferPercentage,
				}
				if err := createProvisionerPod(va, cs.snapshotTimeout); err != nil {
					klog.Errorf("error creating provisioner pod :%v", err)
					return nil, err
				}
			}
		default:
			return nil, status.Errorf(codes.InvalidArgument, "%v not a proper volume source", volumeSource)
		}
		klog.Infof("successfully populated volume %s", req.GetName())
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

	volume, err := cs.kubeClient.CoreV1().PersistentVolumes().Get(context.Background(), volID, metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	}
	klog.V(4).Infof("volume %s to be deleted", volume)
	ns := volume.Spec.NodeAffinity.Required.NodeSelectorTerms
	node := ns[0].MatchExpressions[0].Values[0]

	_, err = cs.kubeClient.CoreV1().Nodes().Get(context.Background(), node, metav1.GetOptions{})
	if err != nil {
		if k8serror.IsNotFound(err) {
			klog.Infof("node %s not found anymore. Assuming volume %s is gone for good.", node, volID)
			return &csi.DeleteVolumeResponse{}, nil
		}
	}

	klog.V(4).Infof("from node %s ", node)

	va := volumeAction{
		action:           actionTypeDelete,
		name:             req.GetVolumeId(),
		nodeName:         node,
		pullPolicy:       cs.pullPolicy,
		provisionerImage: cs.provisionerImage,
		kubeClient:       cs.kubeClient,
		namespace:        cs.namespace,
		vgName:           cs.vgName,
	}
	if err := createProvisionerPod(va, cs.lvmTimeout); err != nil {
		klog.Errorf("error creating provisioner pod :%v", err)
		return nil, err
	}

	klog.V(4).Infof("volume %s successfully deleted", volID)

	return &csi.DeleteVolumeResponse{}, nil
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

func (cs *controllerServer) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT); err != nil {
		klog.V(3).Infof("invalid create snapshot req: %v", req)
		return nil, err
	}

	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}
	// Check arguments
	if len(req.GetSourceVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "SourceVolumeId missing in request")
	}

	s3, err := secretsToS3Parameter(req.Secrets)
	if err != nil {
		return nil, err
	}
	// Need to check for already existing snapshot name, and if found check for the
	// requested sourceVolumeId and sourceVolumeId of snapshot that has been created.
	if snapshots, err := s3ListSnapshots(req.GetName(), req.GetSourceVolumeId(), s3); err == nil && len(snapshots) == 1 {
		return &csi.CreateSnapshotResponse{
			Snapshot: &csi.Snapshot{
				SnapshotId:     req.GetName(),
				SourceVolumeId: req.GetSourceVolumeId(),
				CreationTime:   timestamppb.New(snapshots[0].Time),
				SizeBytes:      snapshots[0].Size,
				ReadyToUse:     true,
			},
		}, nil
	}

	volume, err := cs.kubeClient.CoreV1().PersistentVolumes().Get(context.Background(), req.GetSourceVolumeId(), metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	}
	ns := volume.Spec.NodeAffinity.Required.NodeSelectorTerms
	node := ns[0].MatchExpressions[0].Values[0]

	va := volumeAction{
		action:                      actionTypeCreateSnapshot,
		name:                        req.GetSourceVolumeId(),
		snapshotName:                req.GetName(),
		nodeName:                    node,
		pullPolicy:                  cs.pullPolicy,
		provisionerImage:            cs.provisionerImage,
		kubeClient:                  cs.kubeClient,
		namespace:                   cs.namespace,
		vgName:                      cs.vgName,
		size:                        int64(volume.Size()),
		S3Parameter:                 s3,
		lvmSnapshotBufferPercentage: cs.lvmSnapshotBufferPercentage,
	}
	if err := createProvisionerPod(va, cs.snapshotTimeout); err != nil {
		klog.Errorf("error creating provisioner pod :%v", err)
		return nil, err
	}

	snapshots, err := s3ListSnapshots(req.GetName(), req.GetSourceVolumeId(), s3)
	if err == nil && len(snapshots) == 1 {
		return &csi.CreateSnapshotResponse{
			Snapshot: &csi.Snapshot{
				SnapshotId:     req.GetName(),
				SourceVolumeId: req.GetSourceVolumeId(),
				CreationTime:   timestamppb.New(snapshots[0].Time),
				SizeBytes:      snapshots[0].Size,
				ReadyToUse:     true,
			},
		}, nil
	}
	klog.Errorf("snapshot %s not found in: %v", snapshots)

	return nil, fmt.Errorf("failed to create snapshot %s from volume %s", req.GetName(), req.GetSourceVolumeId())
}

func (cs *controllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	// Check arguments
	if len(req.GetSnapshotId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Snapshot ID missing in request")
	}

	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT); err != nil {
		klog.Infof("invalid delete snapshot req: %v", req)
		return nil, err
	}
	s3, err := secretsToS3Parameter(req.Secrets)
	if err != nil {
		return nil, err
	}

	_, err = DeleteS3Snapshot(req.GetSnapshotId(), s3)
	return &csi.DeleteSnapshotResponse{}, err
}

func (cs *controllerServer) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS); err != nil {
		klog.Infof("invalid list snapshot req: %v", req)
		return nil, err
	}

	s3, err := secretsToS3Parameter(req.Secrets)
	if err != nil {
		return nil, err
	}

	// case 1: SnapshotId is not empty, return snapshots that match the snapshot id.
	if len(req.GetSnapshotId()) != 0 {
		if snapshots, err := s3ListSnapshots(req.GetSnapshotId(), "", s3); err == nil && len(snapshots) == 1 {
			return convertSnapshot(snapshots[0]), nil
		}
	}

	// case 2: SourceVolumeId is not empty, return snapshots that match the source volume id.
	if len(req.GetSourceVolumeId()) != 0 {
		if snapshots, err := s3ListSnapshots("", req.GetSourceVolumeId(), s3); err == nil && len(snapshots) == 1 {
			return convertSnapshot(snapshots[0]), nil
		}
	}

	// case 3: no parameter is set, so we return all the snapshots.
	var snapshots []csi.Snapshot
	s3snapshots, err := s3ListSnapshots("", req.GetSourceVolumeId(), s3)
	if err != nil {
		return nil, err
	}

	for _, s := range s3snapshots {
		snapshot := csi.Snapshot{
			SnapshotId:     s.SnapshotName,
			SourceVolumeId: s.VolumeName,
			CreationTime:   timestamppb.New(s.Time),
			SizeBytes:      s.Size,
			ReadyToUse:     true,
		}
		snapshots = append(snapshots, snapshot)
	}

	var (
		ulenSnapshots = int32(len(snapshots))
		maxEntries    = req.MaxEntries
		startingToken int32
	)

	if v := req.StartingToken; v != "" {
		i, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return nil, status.Errorf(
				codes.Aborted,
				"startingToken=%d !< int32=%d",
				startingToken, math.MaxUint32)
		}
		startingToken = int32(i)
	}

	if startingToken > ulenSnapshots {
		return nil, status.Errorf(
			codes.Aborted,
			"startingToken=%d > len(snapshots)=%d",
			startingToken, ulenSnapshots)
	}

	// Discern the number of remaining entries.
	rem := ulenSnapshots - startingToken

	// If maxEntries is 0 or greater than the number of remaining entries then
	// set maxEntries to the number of remaining entries.
	if maxEntries == 0 || maxEntries > rem {
		maxEntries = rem
	}

	var (
		i       int
		j       = startingToken
		entries = make(
			[]*csi.ListSnapshotsResponse_Entry,
			maxEntries)
	)

	for i = 0; i < len(entries); i++ {
		entries[i] = &csi.ListSnapshotsResponse_Entry{
			Snapshot: &snapshots[j],
		}
		j++
	}

	var nextToken string
	if j < ulenSnapshots {
		nextToken = fmt.Sprintf("%d", j)
	}

	return &csi.ListSnapshotsResponse{
		Entries:   entries,
		NextToken: nextToken,
	}, nil
}

func (cs *controllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func convertSnapshot(s s3Snapshot) *csi.ListSnapshotsResponse {
	entries := []*csi.ListSnapshotsResponse_Entry{
		{
			Snapshot: &csi.Snapshot{
				SnapshotId:     s.SnapshotName,
				SourceVolumeId: s.VolumeName,
				CreationTime:   timestamppb.New(s.Time),
				SizeBytes:      s.Size,
				ReadyToUse:     true,
			},
		},
	}

	rsp := &csi.ListSnapshotsResponse{
		Entries: entries,
	}

	return rsp
}
