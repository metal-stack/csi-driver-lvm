package server

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateStripeSize(t *testing.T) {
	pageSizeKiB := strconv.Itoa(os.Getpagesize() / 1024)
	for _, value := range []string{"", "  ", pageSizeKiB, "64", " 256K ", "1m", "65536b", "128s", ".5M", "+64.0k", "1t"} {
		t.Run(value, func(t *testing.T) {
			want := strings.TrimSpace(value)
			got, err := validateStripeSize("striped", value)
			if err != nil || got != want {
				t.Fatalf("validateStripeSize(%q) = (%q, %v), want (%q, nil)", value, got, err, want)
			}
		})
	}

	for _, value := range []string{"0", "1b", "2k", "96k", "-64k", "64Ki", "64 k", "64K", "bad", "1/2m", "64.000000000000000001k", "2t", "1e", "64." + strings.Repeat("0", 64) + "k"} {
		t.Run("invalid/"+value, func(t *testing.T) {
			got, err := validateStripeSize("striped", value)
			if got != "" || status.Code(err) != codes.InvalidArgument {
				t.Fatalf("validateStripeSize(%q) = (%q, %v), want (empty, InvalidArgument)", value, got, err)
			}
		})
	}

	for _, lvmType := range []string{"linear", "mirror"} {
		t.Run(lvmType, func(t *testing.T) {
			got, err := validateStripeSize(lvmType, "invalid")
			if got != "" || err != nil {
				t.Fatalf("validateStripeSize(%q, \"invalid\") = (%q, %v), want (empty, nil)", lvmType, got, err)
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
		t.Fatalf("error should identify the parameter and constraint: %s", message)
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
