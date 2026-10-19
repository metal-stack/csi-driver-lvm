package server

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// paramSnapshotSize sets an absolute COW size for thick snapshots, e.g. "2Gi".
	paramSnapshotSize = "snapshotSize"
	// paramSnapshotSizePercent sets the COW size relative to the origin size.
	paramSnapshotSizePercent = "snapshotSizePercent"

	defaultSnapshotSizePercent = 100
	maxSnapshotSizePercent     = 100
)

func (d *Driver) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "snapshot name missing in request")
	}
	sourceVolumeID := req.GetSourceVolumeId()
	if len(sourceVolumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "source volume id missing in request")
	}

	release, err := d.volumeLocks.acquire(sourceVolumeID)
	if err != nil {
		return nil, err
	}
	defer release()

	lvName := snapshotLVName(req.GetName())

	existing, err := lvm.GetLV(d.log, d.vgName, lvName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unable to look up snapshot %s: %v", lvName, err)
	}
	if existing != nil {
		return d.existingSnapshotResponse(*existing, sourceVolumeID)
	}

	source, err := lvm.GetLV(d.log, d.vgName, sourceVolumeID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unable to look up source volume %s: %v", sourceVolumeID, err)
	}
	if source == nil {
		return nil, status.Errorf(codes.NotFound, "source volume %s not found", sourceVolumeID)
	}

	cowSize, err := snapshotCOWSize(req.GetParameters())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	cowBytes := cowSize.EstimatedBytes(source.Size)

	free, err := lvm.VgStats(d.log, d.vgName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unable to get free space of vg %s: %v", d.vgName, err)
	}
	if cowBytes > free {
		return nil, status.Errorf(codes.ResourceExhausted, "snapshot needs %d bytes but vg %s has only %d bytes free", cowBytes, d.vgName, free)
	}

	d.log.Info("creating snapshot", "name", req.GetName(), "lv", lvName, "source", sourceVolumeID, "cow-bytes", cowBytes)

	tags := []string{
		lvm.SnapshotDriverTag,
		lvm.Tag(lvm.TagSnapshotName, req.GetName()),
		lvm.Tag(lvm.TagSourceVolume, sourceVolumeID),
		lvm.Tag(lvm.TagSourceSize, strconv.FormatInt(source.Size, 10)),
	}
	// Volumes created before volume modes were tagged have no mode to carry over.
	if mode, ok := source.Tags[lvm.TagVolumeMode]; ok {
		tags = append(tags, lvm.Tag(lvm.TagVolumeMode, mode))
	}

	output, err := lvm.CreateSnapshot(d.log, lvm.CreateSnapshotParams{
		VG:     d.vgName,
		Name:   lvName,
		Origin: sourceVolumeID,
		Size:   cowSize,
		Tags:   tags,
	})
	if err != nil && lvm.IsInsufficientSpace(output) {
		return nil, status.Errorf(codes.ResourceExhausted, "not enough space in vg %s for snapshot %s: %s", d.vgName, lvName, output)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unable to create snapshot %s: %v (%s)", lvName, err, output)
	}

	created, err := lvm.GetLV(d.log, d.vgName, lvName)
	if err != nil || created == nil {
		return nil, status.Errorf(codes.Internal, "unable to read back snapshot %s: %v", lvName, err)
	}

	d.log.Info("successfully created snapshot", "lv", lvName)

	return &csi.CreateSnapshotResponse{Snapshot: d.toCSISnapshot(*created)}, nil
}

func (d *Driver) existingSnapshotResponse(lv lvm.LogicalVolume, sourceVolumeID string) (*csi.CreateSnapshotResponse, error) {
	if !lv.HasTag(lvm.SnapshotDriverTag) {
		return nil, status.Errorf(codes.AlreadyExists, "logical volume %s exists but is not a snapshot of this driver", lv.Name)
	}
	if lv.Tags[lvm.TagSourceVolume] != lvm.SanitizeTagValue(sourceVolumeID) {
		return nil, status.Errorf(codes.AlreadyExists, "snapshot %s already exists for source volume %s", lv.Name, lv.Tags[lvm.TagSourceVolume])
	}
	return &csi.CreateSnapshotResponse{Snapshot: d.toCSISnapshot(lv)}, nil
}

func (d *Driver) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	if len(req.GetSnapshotId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "snapshot id missing in request")
	}

	// The CSI spec requires success for IDs that do not resolve to a local snapshot, including malformed ones.
	id, err := parseSnapshotID(req.GetSnapshotId())
	if err != nil {
		d.log.Warn("ignoring delete of malformed snapshot id", "snapshot-id", req.GetSnapshotId(), "error", err)
		return &csi.DeleteSnapshotResponse{}, nil
	}
	if !id.isLocal(d.nodeId, d.vgName) {
		d.log.Info("ignoring delete of snapshot on another node", "snapshot-id", id.String())
		return &csi.DeleteSnapshotResponse{}, nil
	}

	release, err := d.volumeLocks.acquire(id.String())
	if err != nil {
		return nil, err
	}
	defer release()

	lv, err := lvm.GetLV(d.log, d.vgName, id.LV)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unable to look up snapshot %s: %v", id.LV, err)
	}
	if lv == nil {
		return &csi.DeleteSnapshotResponse{}, nil
	}
	if !lv.HasTag(lvm.SnapshotDriverTag) {
		return nil, status.Errorf(codes.FailedPrecondition, "logical volume %s is not a snapshot of this driver", id.LV)
	}
	if d.restores.isSourceInUse(id.String()) {
		return nil, status.Errorf(codes.FailedPrecondition, "snapshot %s is being restored", id.String())
	}

	d.log.Info("deleting snapshot", "snapshot-id", id.String())

	output, err := lvm.RemoveLVS(d.log, d.vgName, id.LV)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unable to delete snapshot %s: %v (%s)", id.String(), err, output)
	}

	d.log.Info("snapshot successfully deleted", "snapshot-id", id.String())

	return &csi.DeleteSnapshotResponse{}, nil
}

func (d *Driver) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	lvs, err := lvm.ListLVs(d.log, d.vgName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unable to list snapshots: %v", err)
	}

	var entries []*csi.ListSnapshotsResponse_Entry
	for _, lv := range lvs {
		if !lv.HasTag(lvm.SnapshotDriverTag) {
			continue
		}
		snapshot := d.toCSISnapshot(lv)
		if !matchesSnapshotFilter(snapshot, req) {
			continue
		}
		if !snapshot.GetReadyToUse() {
			d.log.Warn("snapshot is invalid, its copy-on-write space overflowed", "snapshot-id", snapshot.GetSnapshotId())
		}
		entries = append(entries, &csi.ListSnapshotsResponse_Entry{Snapshot: snapshot})
	}

	slices.SortFunc(entries, func(a, b *csi.ListSnapshotsResponse_Entry) int {
		return strings.Compare(a.GetSnapshot().GetSnapshotId(), b.GetSnapshot().GetSnapshotId())
	})

	page, nextToken, err := paginate(entries, req.GetStartingToken(), req.GetMaxEntries())
	if err != nil {
		return nil, status.Error(codes.Aborted, err.Error())
	}

	return &csi.ListSnapshotsResponse{Entries: page, NextToken: nextToken}, nil
}

func matchesSnapshotFilter(snapshot *csi.Snapshot, req *csi.ListSnapshotsRequest) bool {
	if id := req.GetSnapshotId(); id != "" && id != snapshot.GetSnapshotId() {
		return false
	}
	if source := req.GetSourceVolumeId(); source != "" && lvm.SanitizeTagValue(source) != snapshot.GetSourceVolumeId() {
		return false
	}
	return true
}

// paginate returns the page starting at the index encoded in token and the token of the next page.
func paginate[T any](items []T, token string, maxEntries int32) ([]T, string, error) {
	start, err := parseStartingToken(token, len(items))
	if err != nil {
		return nil, "", err
	}
	if maxEntries < 0 {
		return nil, "", fmt.Errorf("max entries must not be negative")
	}

	end := len(items)
	if maxEntries > 0 {
		end = min(start+int(maxEntries), len(items))
	}

	nextToken := ""
	if end < len(items) {
		nextToken = strconv.Itoa(end)
	}

	return items[start:end], nextToken, nil
}

// parseStartingToken returns the list index encoded in token, which is 0 for an empty token.
func parseStartingToken(token string, total int) (int, error) {
	if token == "" {
		return 0, nil
	}
	start, err := strconv.Atoi(token)
	if err != nil || start < 0 || start > total {
		return 0, fmt.Errorf("invalid starting token %q", token)
	}
	return start, nil
}

func (d *Driver) toCSISnapshot(lv lvm.LogicalVolume) *csi.Snapshot {
	return &csi.Snapshot{
		SnapshotId:     snapshotID{Node: d.nodeId, VG: lv.VG, LV: lv.Name}.String(),
		SourceVolumeId: lv.Tags[lvm.TagSourceVolume],
		SizeBytes:      snapshotSourceSize(lv),
		CreationTime:   timestamppb.New(lv.CreationTime),
		ReadyToUse:     !lv.SnapshotInvalid,
	}
}

// snapshotSourceSize returns the origin size at snapshot time, falling back to the current origin size.
func snapshotSourceSize(lv lvm.LogicalVolume) int64 {
	raw, ok := lv.Tags[lvm.TagSourceSize]
	if !ok {
		return lv.OriginSize
	}
	size, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return lv.OriginSize
	}
	return size
}

// snapshotCOWSize returns the copy-on-write size for a thick snapshot from the snapshot class parameters.
func snapshotCOWSize(params map[string]string) (lvm.SnapshotSize, error) {
	if raw, ok := params[paramSnapshotSize]; ok {
		return parseAbsoluteSnapshotSize(raw)
	}

	raw, ok := params[paramSnapshotSizePercent]
	if !ok {
		return lvm.SnapshotSize{PercentOfOrigin: defaultSnapshotSizePercent}, nil
	}
	percent, err := strconv.Atoi(raw)
	if err != nil || percent <= 0 || percent > maxSnapshotSizePercent {
		return lvm.SnapshotSize{}, fmt.Errorf("%s must be an integer between 1 and %d, got %q", paramSnapshotSizePercent, maxSnapshotSizePercent, raw)
	}
	return lvm.SnapshotSize{PercentOfOrigin: percent}, nil
}

func parseAbsoluteSnapshotSize(raw string) (lvm.SnapshotSize, error) {
	quantity, err := resource.ParseQuantity(raw)
	if err != nil {
		return lvm.SnapshotSize{}, fmt.Errorf("invalid %s %q: %w", paramSnapshotSize, raw, err)
	}
	if quantity.Value() <= 0 {
		return lvm.SnapshotSize{}, fmt.Errorf("%s must be greater than 0", paramSnapshotSize)
	}
	return lvm.SnapshotSize{Bytes: quantity.Value()}, nil
}
