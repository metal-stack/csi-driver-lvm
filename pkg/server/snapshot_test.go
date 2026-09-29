package server

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	testNode = "node-1"
	testVG   = "csi-lvm"
)

func newTestDriver() *Driver {
	return &Driver{
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		nodeId:      testNode,
		vgName:      testVG,
		volumeLocks: newIDLocker(),
		restores:    newRestoreTracker(),
	}
}

func expectCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("got code %s (%v), want %s", got, err, want)
	}
}

func TestPaginate(t *testing.T) {
	items := []int{0, 1, 2, 3, 4}

	tests := []struct {
		name       string
		token      string
		maxEntries int32
		want       []int
		wantNext   string
		wantErr    bool
	}{
		{name: "all", want: items},
		{name: "first page", maxEntries: 2, want: []int{0, 1}, wantNext: "2"},
		{name: "middle page", token: "2", maxEntries: 2, want: []int{2, 3}, wantNext: "4"},
		{name: "last page", token: "4", maxEntries: 2, want: []int{4}},
		{name: "rest without max", token: "2", want: []int{2, 3, 4}},
		{name: "token at end", token: "5", want: []int{}},
		{name: "max larger than items", maxEntries: 10, want: items},
		{name: "non numeric token", token: "abc", wantErr: true},
		{name: "negative token", token: "-1", wantErr: true},
		{name: "token out of range", token: "6", wantErr: true},
		{name: "negative max", maxEntries: -1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, next, err := paginate(items, tt.token, tt.maxEntries)
			if (err != nil) != tt.wantErr {
				t.Fatalf("paginate error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("page = %v, want %v", got, tt.want)
			}
			if next != tt.wantNext {
				t.Errorf("next token = %q, want %q", next, tt.wantNext)
			}
		})
	}
}

func TestSnapshotCOWSize(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]string
		want    lvm.SnapshotSize
		wantErr bool
	}{
		{name: "default", want: lvm.SnapshotSize{PercentOfOrigin: 100}},
		{name: "percent", params: map[string]string{paramSnapshotSizePercent: "30"}, want: lvm.SnapshotSize{PercentOfOrigin: 30}},
		{name: "absolute", params: map[string]string{paramSnapshotSize: "1Gi"}, want: lvm.SnapshotSize{Bytes: 1 << 30}},
		{name: "absolute wins over percent", params: map[string]string{paramSnapshotSize: "1Mi", paramSnapshotSizePercent: "30"}, want: lvm.SnapshotSize{Bytes: 1 << 20}},
		{name: "percent zero", params: map[string]string{paramSnapshotSizePercent: "0"}, wantErr: true},
		{name: "percent above 100", params: map[string]string{paramSnapshotSizePercent: "101"}, wantErr: true},
		{name: "percent not a number", params: map[string]string{paramSnapshotSizePercent: "half"}, wantErr: true},
		{name: "absolute invalid", params: map[string]string{paramSnapshotSize: "lots"}, wantErr: true},
		{name: "absolute zero", params: map[string]string{paramSnapshotSize: "0"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := snapshotCOWSize(tt.params)
			if (err != nil) != tt.wantErr {
				t.Fatalf("snapshotCOWSize error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("snapshotCOWSize = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestToCSISnapshot(t *testing.T) {
	d := newTestDriver()
	created := time.Unix(1790154631, 0)

	lv := lvm.LogicalVolume{
		Name:         "snap-abc",
		VG:           testVG,
		Origin:       "pvc-1",
		Size:         8 << 20,
		OriginSize:   128 << 20,
		CreationTime: created,
		Tags: map[string]string{
			lvm.SnapshotDriverTag:   "",
			lvm.TagSourceVolume:     "pvc-1",
			lvm.TagSourceSize:       "67108864",
			lvm.TagSnapshotName:     "snapshot-1",
			"unrelated-tag-for-fun": "x",
		},
	}

	got := d.toCSISnapshot(lv)

	if want := "node-1#csi-lvm#snap-abc"; got.GetSnapshotId() != want {
		t.Errorf("snapshot id = %q, want %q", got.GetSnapshotId(), want)
	}
	if got.GetSourceVolumeId() != "pvc-1" {
		t.Errorf("source volume = %q, want pvc-1", got.GetSourceVolumeId())
	}
	// The size at snapshot time wins over the current origin size.
	if got.GetSizeBytes() != 64<<20 {
		t.Errorf("size = %d, want %d", got.GetSizeBytes(), 64<<20)
	}
	if !got.GetCreationTime().AsTime().Equal(created) {
		t.Errorf("creation time = %v, want %v", got.GetCreationTime().AsTime(), created)
	}
	if !got.GetReadyToUse() {
		t.Error("expected snapshot to be ready")
	}

	lv.SnapshotInvalid = true
	delete(lv.Tags, lvm.TagSourceSize)
	got = d.toCSISnapshot(lv)

	if got.GetReadyToUse() {
		t.Error("expected invalid snapshot not to be ready")
	}
	if got.GetSizeBytes() != 128<<20 {
		t.Errorf("size without tag = %d, want origin size %d", got.GetSizeBytes(), 128<<20)
	}
}

func TestMatchesSnapshotFilter(t *testing.T) {
	snapshot := &csi.Snapshot{SnapshotId: "node-1#csi-lvm#snap-abc", SourceVolumeId: "pvc-1"}

	tests := []struct {
		name string
		req  *csi.ListSnapshotsRequest
		want bool
	}{
		{name: "no filter", req: &csi.ListSnapshotsRequest{}, want: true},
		{name: "matching id", req: &csi.ListSnapshotsRequest{SnapshotId: "node-1#csi-lvm#snap-abc"}, want: true},
		{name: "other id", req: &csi.ListSnapshotsRequest{SnapshotId: "node-1#csi-lvm#snap-def"}},
		{name: "matching source", req: &csi.ListSnapshotsRequest{SourceVolumeId: "pvc-1"}, want: true},
		{name: "other source", req: &csi.ListSnapshotsRequest{SourceVolumeId: "pvc-2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesSnapshotFilter(snapshot, tt.req); got != tt.want {
				t.Errorf("matchesSnapshotFilter = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateSnapshotValidation(t *testing.T) {
	d := newTestDriver()

	_, err := d.CreateSnapshot(context.Background(), &csi.CreateSnapshotRequest{SourceVolumeId: "pvc-1"})
	expectCode(t, err, codes.InvalidArgument)

	_, err = d.CreateSnapshot(context.Background(), &csi.CreateSnapshotRequest{Name: "snapshot-1"})
	expectCode(t, err, codes.InvalidArgument)
}

func TestCreateSnapshotLockContention(t *testing.T) {
	d := newTestDriver()
	release, err := d.volumeLocks.acquire("pvc-1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	_, err = d.CreateSnapshot(context.Background(), &csi.CreateSnapshotRequest{Name: "snapshot-1", SourceVolumeId: "pvc-1"})
	expectCode(t, err, codes.Aborted)
}

func TestDeleteSnapshotWithoutLocalSnapshot(t *testing.T) {
	d := newTestDriver()

	_, err := d.DeleteSnapshot(context.Background(), &csi.DeleteSnapshotRequest{})
	expectCode(t, err, codes.InvalidArgument)

	for _, id := range []string{"reallyfakesnapshotid", "node-2#csi-lvm#snap-abc", "node-1#other-vg#snap-abc"} {
		if _, err := d.DeleteSnapshot(context.Background(), &csi.DeleteSnapshotRequest{SnapshotId: id}); err != nil {
			t.Errorf("DeleteSnapshot(%q) = %v, want success", id, err)
		}
	}
}

func TestRestoreVolumeValidation(t *testing.T) {
	d := newTestDriver()

	restoreReq := func(snapshotID string) *csi.CreateVolumeRequest {
		return &csi.CreateVolumeRequest{
			Name:       "pvc-restore",
			Parameters: map[string]string{"type": "linear"},
			VolumeCapabilities: []*csi.VolumeCapability{{
				AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
			}},
			VolumeContentSource: &csi.VolumeContentSource{
				Type: &csi.VolumeContentSource_Snapshot{
					Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapshotID},
				},
			},
		}
	}

	_, err := d.CreateVolume(context.Background(), restoreReq("non-existing-snapshot-id"))
	expectCode(t, err, codes.NotFound)

	_, err = d.CreateVolume(context.Background(), restoreReq("node-2#csi-lvm#snap-abc"))
	expectCode(t, err, codes.ResourceExhausted)

	cloneReq := restoreReq("")
	cloneReq.VolumeContentSource = &csi.VolumeContentSource{
		Type: &csi.VolumeContentSource_Volume{Volume: &csi.VolumeContentSource_VolumeSource{VolumeId: "pvc-1"}},
	}
	_, err = d.CreateVolume(context.Background(), cloneReq)
	expectCode(t, err, codes.InvalidArgument)
}

func TestCheckVolumeMode(t *testing.T) {
	tagged := func(mode string) lvm.LogicalVolume {
		return lvm.LogicalVolume{Name: "snap-abc", Tags: map[string]string{lvm.TagVolumeMode: mode}}
	}

	if err := checkVolumeMode(tagged(lvm.VolumeModeBlock), lvm.VolumeModeBlock); err != nil {
		t.Errorf("block to block: %v", err)
	}
	if err := checkVolumeMode(tagged(lvm.VolumeModeFilesystem), lvm.VolumeModeFilesystem); err != nil {
		t.Errorf("filesystem to filesystem: %v", err)
	}
	// Snapshots from before volume modes were tagged cannot be checked.
	if err := checkVolumeMode(lvm.LogicalVolume{Tags: map[string]string{}}, lvm.VolumeModeBlock); err != nil {
		t.Errorf("untagged snapshot: %v", err)
	}

	expectCode(t, checkVolumeMode(tagged(lvm.VolumeModeBlock), lvm.VolumeModeFilesystem), codes.InvalidArgument)
	expectCode(t, checkVolumeMode(tagged(lvm.VolumeModeFilesystem), lvm.VolumeModeBlock), codes.InvalidArgument)
}
