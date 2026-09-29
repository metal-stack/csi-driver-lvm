package lvm

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// SnapshotSize is either an absolute COW size or a percentage of the origin size.
type SnapshotSize struct {
	Bytes           int64
	PercentOfOrigin int
}

// EstimatedBytes returns the COW size for the given origin, excluding LVM's exception-store overhead.
func (s SnapshotSize) EstimatedBytes(originBytes int64) int64 {
	if s.Bytes > 0 {
		return min(s.Bytes, originBytes)
	}
	return originBytes * int64(s.PercentOfOrigin) / 100
}

func (s SnapshotSize) lvcreateArgs() []string {
	if s.Bytes > 0 {
		return []string{"-L", fmt.Sprintf("%db", s.Bytes)}
	}
	// %ORIGIN lets LVM add the exception-store overhead, so 100% can never overflow.
	return []string{"-l", fmt.Sprintf("%d%%ORIGIN", s.PercentOfOrigin)}
}

// CreateSnapshotParams describes a thick copy-on-write snapshot to create.
type CreateSnapshotParams struct {
	VG     string
	Name   string
	Origin string
	Size   SnapshotSize
	Tags   []string
}

// CreateSnapshot creates a thick COW snapshot of the origin LV with the given tags.
func CreateSnapshot(log *slog.Logger, params CreateSnapshotParams) (string, error) {
	if params.Size.Bytes <= 0 && params.Size.PercentOfOrigin <= 0 {
		return "", fmt.Errorf("snapshot size must be greater than 0")
	}

	args := []string{"-v", "--yes", "--snapshot", "-n", params.Name}
	args = append(args, params.Size.lvcreateArgs()...)
	for _, tag := range params.Tags {
		args = append(args, flagAddTag, tag)
	}
	args = append(args, qualifiedName(params.VG, params.Origin))

	log.Debug("lvcreate snapshot", "args", args)

	out, err := exec.Command(lvcreateCmd, args...).CombinedOutput()
	return string(out), err
}

// IsInsufficientSpace reports whether lvcreate output indicates the VG ran out of extents.
func IsInsufficientSpace(output string) bool {
	return strings.Contains(output, insufficientFreeSpaceMessage)
}

// ReplaceTagParams describes a tag swap on a logical volume.
type ReplaceTagParams struct {
	VG     string
	Name   string
	OldTag string
	NewTag string
}

// ReplaceTag swaps one tag of an LV for another in a single lvchange call.
func ReplaceTag(log *slog.Logger, params ReplaceTagParams) (string, error) {
	args := []string{flagDelTag, params.OldTag, flagAddTag, params.NewTag, qualifiedName(params.VG, params.Name)}

	log.Debug(lvchangeCmd, "args", args)

	out, err := exec.Command(lvchangeCmd, args...).CombinedOutput()
	return string(out), err
}

// SnapshotsOf returns the snapshots of the given origin LV.
func SnapshotsOf(log *slog.Logger, vg, origin string) ([]LogicalVolume, error) {
	return listLVs(log, vg, selectEquals(lvsSelectOrigin, origin))
}
