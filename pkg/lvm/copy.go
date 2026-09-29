package lvm

import (
	"context"
	"fmt"
	"io"
	"os"
)

const copyChunkBytes = 4 << 20

// CopyDeviceParams describes a block copy between two devices.
type CopyDeviceParams struct {
	Source      string
	Destination string
	Bytes       int64
}

// CopyDevice copies the first Bytes of Source to Destination and syncs it to disk.
func CopyDevice(ctx context.Context, params CopyDeviceParams) error {
	src, err := os.Open(params.Source)
	if err != nil {
		return fmt.Errorf("unable to open source device %s: %w", params.Source, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(params.Destination, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("unable to open destination device %s: %w", params.Destination, err)
	}
	defer dst.Close()

	buf := make([]byte, copyChunkBytes)
	for copied := int64(0); copied < params.Bytes; {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, err := io.CopyBuffer(dst, io.LimitReader(src, min(copyChunkBytes, params.Bytes-copied)), buf)
		if err != nil {
			return fmt.Errorf("unable to copy %s to %s: %w", params.Source, params.Destination, err)
		}
		if n == 0 {
			return fmt.Errorf("unexpected end of source device %s after %d bytes", params.Source, copied)
		}
		copied += n
	}

	if err := dst.Sync(); err != nil {
		return fmt.Errorf("unable to sync destination device %s: %w", params.Destination, err)
	}

	return nil
}
