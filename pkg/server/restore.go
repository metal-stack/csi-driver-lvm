package server

import (
	"context"
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// restoreParams describes a CreateVolume request with a snapshot content source.
type restoreParams struct {
	Req           *csi.CreateVolumeRequest
	LVMType       string
	Integrity     bool
	RequiredBytes int64
	VolumeMode    string
}

// SnapshotID returns the ID of the snapshot the volume is restored from.
func (p restoreParams) SnapshotID() string {
	return p.Req.GetVolumeContentSource().GetSnapshot().GetSnapshotId()
}

// restoreVolume creates a new volume and block-copies the snapshot into it, leaving the snapshot untouched.
func (d *Driver) restoreVolume(ctx context.Context, params restoreParams) (*csi.CreateVolumeResponse, error) {
	var (
		name  = params.Req.GetName()
		rawID = params.SnapshotID()
	)

	id, err := parseSnapshotID(rawID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "snapshot %s not found: %v", rawID, err)
	}
	if !id.isLocal(d.nodeId, d.vgName) {
		return nil, status.Errorf(codes.ResourceExhausted, "snapshot %s lives on node %s and cannot be restored on node %s", rawID, id.Node, d.nodeId)
	}

	release, err := d.volumeLocks.acquire(name)
	if err != nil {
		return nil, err
	}
	defer release()

	// Held until the copy is tracked, so DeleteSnapshot cannot remove the snapshot in between.
	releaseSnapshot, err := d.volumeLocks.acquire(id.String())
	if err != nil {
		return nil, err
	}
	defer releaseSnapshot()

	snapshot, err := d.restorableSnapshot(id)
	if err != nil {
		return nil, err
	}
	if err := checkVolumeMode(*snapshot, params.VolumeMode); err != nil {
		return nil, err
	}

	sourceBytes := d.toCSISnapshot(*snapshot).GetSizeBytes()
	requiredBytes := params.RequiredBytes
	if requiredBytes == 0 {
		requiredBytes = sourceBytes
	}
	if requiredBytes < sourceBytes {
		return nil, status.Errorf(codes.OutOfRange, "requested %d bytes but snapshot %s needs at least %d bytes", requiredBytes, rawID, sourceBytes)
	}

	done, err := d.ensureRestoreTarget(ensureRestoreTargetParams{
		Name:          name,
		SnapshotID:    rawID,
		LVMType:       params.LVMType,
		Integrity:     params.Integrity,
		RequiredBytes: requiredBytes,
		VolumeMode:    params.VolumeMode,
	})
	if err != nil {
		return nil, err
	}
	if done {
		return d.createVolumeResponse(params.Req, requiredBytes), nil
	}

	job := d.restores.start(name, rawID, func(ctx context.Context) error {
		return d.copySnapshot(ctx, copySnapshotParams{SnapshotLV: id.LV, TargetLV: name, Bytes: sourceBytes})
	})
	releaseSnapshot()

	select {
	case <-job.done:
	case <-ctx.Done():
		return nil, status.Errorf(codes.Aborted, "restore of %s from snapshot %s is still in progress", name, rawID)
	}

	d.restores.forget(name, job)
	if job.err != nil {
		return nil, status.Errorf(codes.Internal, "unable to restore %s from snapshot %s: %v", name, rawID, job.err)
	}

	d.log.Info("successfully restored volume from snapshot", "name", name, "snapshot-id", rawID)

	return d.createVolumeResponse(params.Req, requiredBytes), nil
}

func (d *Driver) restorableSnapshot(id snapshotID) (*lvm.LogicalVolume, error) {
	snapshot, err := lvm.GetLV(d.log, d.vgName, id.LV)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unable to look up snapshot %s: %v", id.String(), err)
	}
	if snapshot == nil || !snapshot.HasTag(lvm.SnapshotDriverTag) {
		return nil, status.Errorf(codes.NotFound, "snapshot %s not found", id.String())
	}
	if snapshot.SnapshotInvalid {
		return nil, status.Errorf(codes.FailedPrecondition, "snapshot %s is invalid because its copy-on-write space overflowed", id.String())
	}
	return snapshot, nil
}

// checkVolumeMode rejects restores into another volume mode, which would expose or wipe the snapshot's data.
func checkVolumeMode(snapshot lvm.LogicalVolume, volumeMode string) error {
	sourceMode, ok := snapshot.Tags[lvm.TagVolumeMode]
	if !ok || sourceMode == volumeMode {
		return nil
	}
	return status.Errorf(codes.InvalidArgument, "snapshot %s of a %s volume cannot be restored as a %s volume", snapshot.Name, sourceMode, volumeMode)
}

// ensureRestoreTargetParams describes the volume a snapshot is restored into.
type ensureRestoreTargetParams struct {
	Name          string
	SnapshotID    string
	LVMType       string
	Integrity     bool
	RequiredBytes int64
	VolumeMode    string
}

// ensureRestoreTarget creates the restore target if needed and reports whether its copy already finished.
func (d *Driver) ensureRestoreTarget(params ensureRestoreTargetParams) (bool, error) {
	target, err := lvm.GetLV(d.log, d.vgName, params.Name)
	if err != nil {
		return false, status.Errorf(codes.Internal, "unable to look up volume %s: %v", params.Name, err)
	}

	if target == nil {
		return false, d.createRestoreTarget(params)
	}
	if target.Tags[lvm.TagRestoredFrom] != lvm.SanitizeTagValue(params.SnapshotID) {
		return false, status.Errorf(codes.AlreadyExists, "volume %s already exists with a different content source", params.Name)
	}

	// A missing job with an in-progress tag means the driver restarted mid-copy, so the copy starts over.
	return target.Tags[lvm.TagRestoreState] == lvm.RestoreStateDone, nil
}

// createRestoreTarget creates the LV a snapshot is copied into, tagged as an unfinished restore.
func (d *Driver) createRestoreTarget(params ensureRestoreTargetParams) error {
	d.log.Info("creating volume from snapshot", "name", params.Name, "snapshot-id", params.SnapshotID)

	output, err := lvm.CreateLV(d.log, lvm.CreateLVParams{
		VG:        d.vgName,
		Name:      params.Name,
		Size:      uint64(params.RequiredBytes), //nolint:gosec
		Type:      params.LVMType,
		Integrity: params.Integrity,
		Tags: []string{
			lvm.Tag(lvm.TagVolumeMode, params.VolumeMode),
			lvm.Tag(lvm.TagRestoredFrom, params.SnapshotID),
			lvm.Tag(lvm.TagRestoreState, lvm.RestoreStateInProgress),
		},
	})
	if err != nil && lvm.IsInsufficientSpace(output) {
		return status.Errorf(codes.ResourceExhausted, "not enough space in vg %s for volume %s: %s", d.vgName, params.Name, output)
	}
	if err != nil {
		return status.Errorf(codes.Internal, "unable to create lv %s: %v (%s)", params.Name, err, output)
	}

	return nil
}

// copySnapshotParams describes a block copy from a snapshot LV into a restore target LV.
type copySnapshotParams struct {
	SnapshotLV string
	TargetLV   string
	Bytes      int64
}

// copySnapshot copies the snapshot's data into the target LV and marks the restore done.
func (d *Driver) copySnapshot(ctx context.Context, params copySnapshotParams) error {
	source, err := lvm.LVDevicePath(d.log, d.vgName, params.SnapshotLV)
	if err != nil {
		return err
	}
	destination, err := lvm.LVDevicePath(d.log, d.vgName, params.TargetLV)
	if err != nil {
		return err
	}

	d.log.Info("copying snapshot data", "source", source, "destination", destination, "bytes", params.Bytes)

	if err := lvm.CopyDevice(ctx, lvm.CopyDeviceParams{Source: source, Destination: destination, Bytes: params.Bytes}); err != nil {
		return err
	}

	output, err := lvm.ReplaceTag(d.log, lvm.ReplaceTagParams{
		VG:     d.vgName,
		Name:   params.TargetLV,
		OldTag: lvm.Tag(lvm.TagRestoreState, lvm.RestoreStateInProgress),
		NewTag: lvm.Tag(lvm.TagRestoreState, lvm.RestoreStateDone),
	})
	if err != nil {
		return fmt.Errorf("unable to mark restore of %s done: %w (%s)", params.TargetLV, err, output)
	}

	return nil
}
