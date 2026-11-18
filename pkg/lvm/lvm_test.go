package lvm

import (
	"slices"
	"testing"
)

func TestBuildLvcreateArgs(t *testing.T) {
	tests := []struct {
		name       string
		lvmType    string
		pvs        int
		integrity  bool
		stripeSize string
		want       []string
	}{
		{
			name:       "striped with custom stripe size",
			lvmType:    stripedType,
			pvs:        2,
			stripeSize: "0.25M",
			want:       []string{"--type", "striped", "--stripes", "2", "--stripesize", "0.25M"},
		},
		{
			name:    "striped with LVM default",
			lvmType: stripedType,
			pvs:     3,
			want:    []string{"--type", "striped", "--stripes", "3"},
		},
		{
			name:       "striped with single PV falls back to linear",
			lvmType:    stripedType,
			pvs:        1,
			stripeSize: "256k",
		},
		{
			name:       "linear ignores stripe size",
			lvmType:    linearType,
			pvs:        2,
			stripeSize: "256k",
		},
		{
			name:       "mirror ignores stripe size and uses integrity",
			lvmType:    mirrorType,
			pvs:        2,
			integrity:  true,
			stripeSize: "256k",
			want:       []string{"--type", "raid1", "--mirrors", "1", "--nosync", "--raidintegrity", "y"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := []string{"-v", "--yes", "-n", "pvc-123", "-W", "y", "-L", "104857600b"}
			want = append(want, tt.want...)
			want = append(want, "--addtag", "lv.metal-stack.io/csi-lvm-driver", "test-vg")

			got, err := buildLvcreateArgs("test-vg", "pvc-123", 100<<20, tt.lvmType, tt.pvs, tt.integrity, tt.stripeSize)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("buildLvcreateArgs = (%v, %v), want (%v, nil)", got, err, want)
			}
		})
	}
}
