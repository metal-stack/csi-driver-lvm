package lvm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyDevice(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	data := bytes.Repeat([]byte("snapshot"), (copyChunkBytes/8)+1234)
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// The destination is larger than the copy, like a restore into a bigger volume.
	if err := os.WriteFile(dst, make([]byte, len(data)+4096), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CopyDevice(context.Background(), CopyDeviceParams{Source: src, Destination: dst, Bytes: int64(len(data))}); err != nil {
		t.Fatalf("CopyDevice failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:len(data)], data) {
		t.Error("copied data differs from source")
	}
	if !bytes.Equal(got[len(data):], make([]byte, 4096)) {
		t.Error("copy wrote past the requested length")
	}

	if err := CopyDevice(context.Background(), CopyDeviceParams{Source: src, Destination: dst, Bytes: int64(len(data)) + 1}); err == nil {
		t.Error("expected error when source is shorter than requested")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CopyDevice(ctx, CopyDeviceParams{Source: src, Destination: dst, Bytes: int64(len(data))}); err == nil {
		t.Error("expected error for cancelled context")
	}
}
