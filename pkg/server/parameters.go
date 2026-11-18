package server

import (
	"math/big"
	"os"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var stripeSizePattern = regexp.MustCompile(`^\+?([0-9]+(?:\.[0-9]*)?|\.[0-9]+)([bBsSkKmMgGtTpPeE]?)$`)

// validateStripeSize returns the trimmed LVM argument, or an empty string to omit it.
func validateStripeSize(lvmType, value string) (string, error) {
	value = strings.TrimSpace(value)
	if lvmType != "striped" || value == "" {
		return "", nil
	}
	// Bound the work for exact decimal parsing of inline volume attributes.
	if len(value) > 64 {
		return "", status.Error(codes.InvalidArgument, "stripeSize exceeds 64 characters")
	}

	// LVM uses the node's page size as a minimum and stores the size in 32-bit sectors.
	minBytes := uint64(os.Getpagesize())
	const maxBytes = 1 << 40 // Largest power of two representable in 32-bit 512-byte sectors.
	parts := stripeSizePattern.FindStringSubmatch(value)
	if parts != nil {
		shifts := map[string]uint{"": 10, "b": 0, "s": 9, "k": 10, "m": 20, "g": 30, "t": 40, "p": 50, "e": 60}
		size, ok := new(big.Rat).SetString(parts[1])
		if ok {
			size.Mul(size, big.NewRat(1<<shifts[strings.ToLower(parts[2])], 1))
			if size.IsInt() && size.Num().IsUint64() {
				bytes := size.Num().Uint64()
				if bytes >= minBytes && bytes <= maxBytes && bytes&(bytes-1) == 0 {
					return value, nil
				}
			}
		}
	}
	return "", status.Errorf(codes.InvalidArgument, "invalid stripeSize %q: must be a power-of-two LVM size between %d KiB and 1 TiB (e.g. 256k or 1m)", value, minBytes/1024)
}
