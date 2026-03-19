package server

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"context"

	"github.com/docker/go-units"
	v1alpha1 "github.com/metal-stack/csi-driver-lvm/api/v1alpha1"
	"github.com/metal-stack/csi-driver-lvm/pkg/drbd"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"golang.org/x/sys/unix"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
)

const topologyKeyNode = "topology.lvm.csi/node"

func (d *Driver) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	// Check arguments
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability missing in request")
	}
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "target path missing in request")
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
		req.GetVolumeContext()["csi.storage.k8s.io/ephemeral"] == "" && d.ephemeral // Kubernetes 1.15 doesn't have csi.storage.k8s.io/ephemeral.

	// if ephemeral is specified, create volume here
	if ephemeralVolume {
		size, err := parseSize(req.GetVolumeContext()["size"])
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}

		volID := req.GetVolumeId()

		output, err := lvm.CreateVG(d.log, d.vgName, d.devicesPattern)
		if err != nil {
			return nil, fmt.Errorf("unable to create vg: %w output:%s", err, output)
		}

		output, err = lvm.CreateLV(d.log, d.vgName, volID, size, req.GetVolumeContext()["type"], false)
		if err != nil {
			return nil, fmt.Errorf("unable to create lv: %w output:%s", err, output)
		}

		d.log.Info("ephemeral mode: created volume", "volume", volID, "size", size)
	}

	// Check if this is a DRBD-replicated volume
	isDRBD := req.GetVolumeContext()["replication"] == "drbd"
	drbdMinorStr := req.GetVolumeContext()["drbdMinor"]

	if isDRBD && drbdMinorStr != "" && d.k8sClient != nil {
		minor, err := strconv.Atoi(drbdMinorStr)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid drbdMinor: %s", drbdMinorStr)
		}

		// Promote this node to primary for the DRBD resource
		d.log.Info("promoting drbd resource for volume publish", "volume-id", req.GetVolumeId())

		// Check if we need to force-promote (failover scenario)
		drbdStatus, err := drbd.GetStatus(d.log, req.GetVolumeId())
		if err != nil {
			d.log.Warn("could not get drbd status, attempting force promote", "error", err)
			if _, err := drbd.ForcePromote(d.log, req.GetVolumeId()); err != nil {
				return nil, fmt.Errorf("unable to force-promote drbd resource: %w", err)
			}
		} else if drbdStatus.Role != "Primary" {
			if drbdStatus.ConnectionState == "Connected" {
				if _, err := drbd.Promote(d.log, req.GetVolumeId()); err != nil {
					return nil, fmt.Errorf("unable to promote drbd resource: %w", err)
				}
			} else {
				if _, err := drbd.ForcePromote(d.log, req.GetVolumeId()); err != nil {
					return nil, fmt.Errorf("unable to force-promote drbd resource: %w", err)
				}
			}
		}

		// Use the DRBD device path instead of the LV path
		drbdDev := drbd.DevicePath(minor)

		if req.GetVolumeCapability().GetBlock() != nil {
			output, err := lvm.BindMountLVByPath(d.log, drbdDev, targetPath)
			if err != nil {
				return nil, fmt.Errorf("unable to bind mount drbd device: %w output:%s", err, output)
			}
			d.log.Info("block drbd", "id", req.GetVolumeId(), "device", drbdDev, "created at", targetPath)
		} else if req.GetVolumeCapability().GetMount() != nil {
			output, err := lvm.MountLVByPath(d.log, drbdDev, targetPath, req.GetVolumeCapability().GetMount().GetFsType())
			if err != nil {
				return nil, fmt.Errorf("unable to mount drbd device: %w output:%s", err, output)
			}
			d.log.Info("mounted drbd", "id", req.GetVolumeId(), "device", drbdDev, "created at", targetPath)
		}

		return &csi.NodePublishVolumeResponse{}, nil
	}

	if req.GetVolumeCapability().GetBlock() != nil {
		output, err := lvm.BindMountLV(d.log, req.GetVolumeId(), targetPath, d.vgName)
		if err != nil {
			return nil, fmt.Errorf("unable to bind mount lv: %w output:%s", err, output)
		}
		// FIXME: VolumeCapability is a struct and not the size
		d.log.Info("block lv", "id", req.GetVolumeId(), "size", req.GetVolumeCapability(), "vg", d.vgName, "devices", d.devicesPattern, "created at", targetPath)

	} else if req.GetVolumeCapability().GetMount() != nil {
		output, err := lvm.MountLV(d.log, req.GetVolumeId(), targetPath, d.vgName, req.GetVolumeCapability().GetMount().GetFsType())
		if err != nil {
			return nil, fmt.Errorf("unable to mount lv: %w output:%s", err, output)
		}
		// FIXME: VolumeCapability is a struct and not the size
		d.log.Info("mounted lv", "id", req.GetVolumeId(), "size", req.GetVolumeCapability(), "vg", d.vgName, "devices", d.devicesPattern, "created at", targetPath)
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *Driver) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	// implement deletion of ephemeral volumes
	volID := req.GetVolumeId()

	// Check arguments
	if len(volID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "target path missing in request")
	}

	lvm.UmountLV(d.log, req.GetTargetPath())

	// Demote DRBD resource if this is a replicated volume
	if d.k8sClient != nil {
		var dv v1alpha1.DRBDVolume
		if err := d.k8sClient.Get(ctx, types.NamespacedName{Name: volID}, &dv); err == nil {
			d.log.Info("demoting drbd resource after unpublish", "volume-id", volID)
			if _, err := drbd.Demote(d.log, volID); err != nil {
				d.log.Warn("failed to demote drbd resource (may already be secondary)", "error", err)
			}
		}
	}

	// ephemeral volumes start with "csi-"
	if strings.HasPrefix(volID, "csi-") {
		// remove ephemeral volume here
		output, err := lvm.RemoveLVS(d.log, d.vgName, volID)
		if err != nil {
			return nil, fmt.Errorf("unable to delete lv: %w output:%s", err, output)
		}
		d.log.Info("lv deleted", "id", volID, "vg", d.vgName)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *Driver) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume ID missing in request")
	}
	if len(req.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "target path missing in request")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume Capability missing in request")
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *Driver) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume ID missing in request")
	}
	if len(req.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "target path missing in request")
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *Driver) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	topology := &csi.Topology{
		Segments: map[string]string{topologyKeyNode: d.nodeId},
	}

	return &csi.NodeGetInfoResponse{
		NodeId:             d.nodeId,
		MaxVolumesPerNode:  d.maxVolumesPerNode,
		AccessibleTopology: topology,
	}, nil
}

func (d *Driver) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
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

func (d *Driver) NodeGetVolumeStats(ctx context.Context, in *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	var fs unix.Statfs_t

	err := unix.Statfs(in.GetVolumePath(), &fs)
	if err != nil {
		return nil, err
	}

	diskFree := int64(fs.Bfree) * int64(fs.Bsize)   // nolint:gosec
	diskTotal := int64(fs.Blocks) * int64(fs.Bsize) // nolint:gosec

	inodesFree := int64(fs.Ffree)  // nolint:gosec
	inodesTotal := int64(fs.Files) // nolint:gosec

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

func (d *Driver) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	// Check arguments
	if req.GetCapacityRange() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability missing in request")
	}
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume ID missing in request")
	}
	capacity := int64(req.GetCapacityRange().GetRequiredBytes())

	volID := req.GetVolumeId()
	volPath := req.GetVolumePath()
	if len(volPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume path not provided")
	}

	info, err := os.Stat(volPath)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "could not get file information from %s: %v", volPath, err)
	}

	isBlock := false
	m := info.Mode()
	if !m.IsDir() {
		d.log.Warn("volume expand request on block device: filesystem resize has to be done externally")
		isBlock = true
	}

	output, err := lvm.ExtendLVS(d.log, d.vgName, volID, uint64(capacity), isBlock) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("unable to umount lv: %w output:%s", err, output)

	}

	return &csi.NodeExpandVolumeResponse{
		CapacityBytes: capacity,
	}, nil

}

func parseSize(val string) (uint64, error) {
	if val == "" {
		return 0, fmt.Errorf("ephemeral inline volume is missing size parameter")
	}

	parseWithKubernetes := func(raw string) (uint64, error) {
		sizeQuantity, err := resource.ParseQuantity(raw)
		if err != nil {
			return 0, fmt.Errorf("failed to parse size (%s) of ephemeral inline volume: %w", raw, err)
		}

		size, err := strconv.ParseUint(sizeQuantity.AsDec().String(), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parsed volume size (%s) of ephemeral inline volume does not fit into an uint64: %w", raw, err)
		}

		return size, nil
	}

	// this was the initial method to parse the size and has to be kept for compatibility reasons
	parseWithGoUnits := func(raw string) (uint64, error) {
		size, err := units.RAMInBytes(raw)
		if err != nil {
			return 0, fmt.Errorf("failed to parse size (%s) of ephemeral inline volume: %w", raw, err)
		}

		return uint64(size), nil //nolint:gosec
	}

	if size, err := parseWithKubernetes(val); err == nil {
		return size, nil
	}

	return parseWithGoUnits(val)
}
