package server

import (
	"strings"
	"testing"
)

func TestParseSnapshotID(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    snapshotID
		wantErr bool
	}{
		{name: "valid", raw: "node-1#csi-lvm#snap-abc", want: snapshotID{Node: "node-1", VG: "csi-lvm", LV: "snap-abc"}},
		{name: "empty", raw: "", wantErr: true},
		{name: "too few parts", raw: "node-1#snap-abc", wantErr: true},
		{name: "too many parts", raw: "a#b#c#d", wantErr: true},
		{name: "empty node", raw: "#csi-lvm#snap-abc", wantErr: true},
		{name: "empty lv", raw: "node-1#csi-lvm#", wantErr: true},
		{name: "no separator", raw: "reallyfakesnapshotid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSnapshotID(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseSnapshotID(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseSnapshotID(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSnapshotIDRoundTrip(t *testing.T) {
	id := snapshotID{Node: "kind-control-plane", VG: "csi-lvm", LV: snapshotLVName("snapshot-1")}

	parsed, err := parseSnapshotID(id.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed != id {
		t.Fatalf("round trip = %+v, want %+v", parsed, id)
	}
}

func TestSnapshotIDIsLocal(t *testing.T) {
	id := snapshotID{Node: "node-1", VG: "csi-lvm", LV: "snap-abc"}

	if !id.isLocal("node-1", "csi-lvm") {
		t.Error("expected snapshot to be local")
	}
	if id.isLocal("node-2", "csi-lvm") {
		t.Error("expected snapshot on another node not to be local")
	}
	if id.isLocal("node-1", "other-vg") {
		t.Error("expected snapshot in another vg not to be local")
	}
}

func TestSnapshotLVName(t *testing.T) {
	longName := strings.Repeat("x", 128)

	for _, name := range []string{"snapshot-8a4e2c1f-0000-4000-8000-000000000000", longName, "a"} {
		got := snapshotLVName(name)

		if got != snapshotLVName(name) {
			t.Errorf("snapshotLVName(%q) is not deterministic", name)
		}
		if !strings.HasPrefix(got, snapshotLVPrefix) {
			t.Errorf("snapshotLVName(%q) = %q, missing prefix %q", name, got, snapshotLVPrefix)
		}
		if strings.HasPrefix(got, "snapshot") || strings.HasPrefix(got, "csi-") {
			t.Errorf("snapshotLVName(%q) = %q uses a reserved prefix", name, got)
		}
		if want := len(snapshotLVPrefix) + snapshotLVHashLen; len(got) != want {
			t.Errorf("snapshotLVName(%q) has length %d, want %d", name, len(got), want)
		}
	}

	if snapshotLVName("a") == snapshotLVName("b") {
		t.Error("different names must derive different LV names")
	}
}
