package lvm

import (
	"testing"
	"time"
)

// Captured from lvs 2.03.35 with the flags used by listLVs.
const lvsFixture = `  {
      "report": [
          {
              "lv": [
                  {"lv_name":"pvc-1", "vg_name":"csi-lvm", "origin":"", "origin_size":"67108864", "lv_size":"67108864", "lv_time":"1790154631", "lv_tags":"lv.metal-stack.io/csi-lvm-driver", "lv_snapshot_invalid":"-1", "lv_attr":"owi-a-s---"},
                  {"lv_name":"snap-abc", "vg_name":"csi-lvm", "origin":"pvc-1", "origin_size":"67108864", "lv_size":"8388608", "lv_time":"1790154632", "lv_tags":"snapshot.metal-stack.io/csi-lvm-driver,csi-source-volume=pvc-1,csi-source-size=67108864", "lv_snapshot_invalid":"1", "lv_attr":"swi-I-s---"}
              ]
          }
      ]
      ,
      "log": [
      ]
  }`

func TestParseLVReport(t *testing.T) {
	lvs, err := parseLVReport([]byte(lvsFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lvs) != 2 {
		t.Fatalf("got %d lvs, want 2", len(lvs))
	}

	origin, snapshot := lvs[0], lvs[1]

	if origin.IsSnapshot() || origin.SnapshotInvalid {
		t.Errorf("origin parsed as snapshot: %+v", origin)
	}
	if !origin.HasTag(VolumeDriverTag) {
		t.Errorf("origin is missing %s: %v", VolumeDriverTag, origin.Tags)
	}

	if !snapshot.IsSnapshot() || snapshot.Origin != "pvc-1" {
		t.Errorf("snapshot origin = %q, want pvc-1", snapshot.Origin)
	}
	if !snapshot.SnapshotInvalid {
		t.Error("expected snapshot to be invalid")
	}
	if snapshot.Size != 8388608 || snapshot.OriginSize != 67108864 {
		t.Errorf("sizes = %d/%d, want 8388608/67108864", snapshot.Size, snapshot.OriginSize)
	}
	if !snapshot.CreationTime.Equal(time.Unix(1790154632, 0)) {
		t.Errorf("creation time = %v", snapshot.CreationTime)
	}
	if !snapshot.HasTag(SnapshotDriverTag) || snapshot.Tags[TagSourceVolume] != "pvc-1" || snapshot.Tags[TagSourceSize] != "67108864" {
		t.Errorf("unexpected tags: %v", snapshot.Tags)
	}
}

func TestParseLVReportEmptyAndInvalid(t *testing.T) {
	lvs, err := parseLVReport([]byte(`{"report":[{"lv":[]}]}`))
	if err != nil || len(lvs) != 0 {
		t.Fatalf("empty report = %v, %v", lvs, err)
	}

	if _, err := parseLVReport([]byte(`not json`)); err == nil {
		t.Error("expected error for invalid json")
	}

	if _, err := parseLVReport([]byte(`{"report":[{"lv":[{"lv_name":"x","lv_size":"big"}]}]}`)); err == nil {
		t.Error("expected error for non numeric size")
	}
}

func TestTag(t *testing.T) {
	tests := map[string]string{
		"pvc-1":                   "csi-source-volume=pvc-1",
		"node-1#csi-lvm#snap-abc": "csi-source-volume=node-1#csi-lvm#snap-abc",
		"has space,comma@at":      "csi-source-volume=has_space_comma_at",
	}

	for value, want := range tests {
		if got := Tag(TagSourceVolume, value); got != want {
			t.Errorf("Tag(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestSnapshotSize(t *testing.T) {
	if got := (SnapshotSize{PercentOfOrigin: 25}).EstimatedBytes(400); got != 100 {
		t.Errorf("25%% of 400 = %d, want 100", got)
	}
	if got := (SnapshotSize{Bytes: 1000}).EstimatedBytes(400); got != 400 {
		t.Errorf("absolute size is not capped at origin: %d", got)
	}
	if args := (SnapshotSize{PercentOfOrigin: 100}).lvcreateArgs(); args[0] != "-l" || args[1] != "100%ORIGIN" {
		t.Errorf("unexpected args %v", args)
	}
	if args := (SnapshotSize{Bytes: 4096}).lvcreateArgs(); args[0] != "-L" || args[1] != "4096b" {
		t.Errorf("unexpected args %v", args)
	}
}
