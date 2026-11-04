package server

import (
	"math/big"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var stripeSizePattern = regexp.MustCompile(`^\+?([0-9]+(?:\.[0-9]*)?|\.[0-9]+)([bBsSkKmMgGtTpPeE]?)$`)

func validateStripeSize(lvmType, value string) error {
	s := strings.TrimSpace(value)
	if lvmType != "striped" || s == "" {
		return nil
	}

	invalid := status.Errorf(codes.InvalidArgument, "invalid stripeSize %q: must be a power-of-two LVM size of at least 4 KiB (e.g. 64k, 256k, 1m)", value)
	parts := stripeSizePattern.FindStringSubmatch(s)
	if parts == nil {
		return invalid
	}

	shift := map[string]uint{"": 10, "b": 0, "s": 9, "k": 10, "m": 20, "g": 30, "t": 40, "p": 50, "e": 60}[strings.ToLower(parts[2])]
	size, ok := new(big.Rat).SetString(parts[1])
	if !ok {
		return invalid
	}
	size.Mul(size, big.NewRat(1<<shift, 1))
	if !size.IsInt() || !size.Num().IsUint64() {
		return invalid
	}
	bytes := size.Num().Uint64()
	if bytes < 4096 || bytes&(bytes-1) != 0 {
		return invalid
	}
	return nil
}
