package server

import (
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateStripeSize(t *testing.T) {
	for _, value := range []string{"", "   ", "4k", "64", "64k", "256K", "1m", "1M", "  256k  ", "65536b", "128s", "0.5m", ".5M", "+64k", "64.0k"} {
		t.Run("valid/"+value, func(t *testing.T) {
			if err := validateStripeSize("striped", value); err != nil {
				t.Fatal(err)
			}
		})
	}

	for _, value := range []string{"3", "3k", "96k", "1.5m", "0", "0k", "-64k", "1k", "1b", "64Ki", "64kb", "64 k", "nope", "k", "NaN", "Inf", "1/2m", "0x40k", "64.000000000000000001k", "18446744073709551616b"} {
		t.Run("invalid/"+value, func(t *testing.T) {
			err := validateStripeSize("striped", value)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("got %v, want InvalidArgument", err)
			}
		})
	}

	for _, lvmType := range []string{"linear", "mirror"} {
		t.Run(lvmType, func(t *testing.T) {
			if err := validateStripeSize(lvmType, "invalid"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCreateVolumeInvalidStripeSize(t *testing.T) {
	d := &Driver{}
	resp, err := d.CreateVolume(t.Context(), &csi.CreateVolumeRequest{
		Name: "test-volume",
		VolumeCapabilities: []*csi.VolumeCapability{{
			AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
		}},
		Parameters: map[string]string{"type": "striped", "stripeSize": "96k"},
	})
	if resp != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("got (%v, %v), want (nil, InvalidArgument)", resp, err)
	}
	if message := status.Convert(err).Message(); !strings.Contains(message, `stripeSize "96k"`) || !strings.Contains(message, "power-of-two") {
		t.Fatalf("error should identify the invalid value and constraint: %s", message)
	}
}

func TestNodePublishVolumeInvalidStripeSize(t *testing.T) {
	d := &Driver{}
	resp, err := d.NodePublishVolume(t.Context(), &csi.NodePublishVolumeRequest{
		VolumeId:   "test-volume",
		TargetPath: "/test-volume",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
		},
		VolumeContext: map[string]string{
			"csi.storage.k8s.io/ephemeral": "true",
			"size":                         "1Gi",
			"type":                         "striped",
			"stripeSize":                   "96k",
		},
	})
	if resp != nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("got (%v, %v), want (nil, InvalidArgument)", resp, err)
	}
}
