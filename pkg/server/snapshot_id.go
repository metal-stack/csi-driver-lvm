package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

const (
	snapshotIDSeparator = "#"
	snapshotIDParts     = 3
	// snapshotLVPrefix avoids the "snapshot" prefix LVM reserves and the "csi-" prefix of ephemeral volumes.
	snapshotLVPrefix  = "snap-"
	snapshotLVHashLen = 32
)

// snapshotID locates a snapshot LV without a lookup table.
type snapshotID struct {
	Node string
	VG   string
	LV   string
}

func (id snapshotID) String() string {
	return strings.Join([]string{id.Node, id.VG, id.LV}, snapshotIDSeparator)
}

// isLocal reports whether the snapshot lives in the VG served by this driver instance.
func (id snapshotID) isLocal(node, vg string) bool {
	return id.Node == node && id.VG == vg
}

func parseSnapshotID(raw string) (snapshotID, error) {
	parts := strings.Split(raw, snapshotIDSeparator)
	if len(parts) != snapshotIDParts {
		return snapshotID{}, fmt.Errorf("snapshot id %q must have the form <node>%s<vg>%s<lv>", raw, snapshotIDSeparator, snapshotIDSeparator)
	}
	if slices.Contains(parts, "") {
		return snapshotID{}, fmt.Errorf("snapshot id %q contains an empty part", raw)
	}

	return snapshotID{Node: parts[0], VG: parts[1], LV: parts[2]}, nil
}

// snapshotLVName derives a valid, deterministic LV name from the CSI snapshot name.
func snapshotLVName(name string) string {
	sum := sha256.Sum256([]byte(name))
	return snapshotLVPrefix + hex.EncodeToString(sum[:])[:snapshotLVHashLen]
}
