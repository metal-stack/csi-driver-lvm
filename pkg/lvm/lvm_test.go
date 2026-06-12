package lvm

import "testing"

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func hasFlagValue(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestBuildLvcreateArgs(t *testing.T) {
	const (
		name = "pvc-123"
		size = uint64(104857600) // 100Mi
	)

	tests := []struct {
		name       string
		lvmType    string
		pvs        int
		integrity  bool
		stripeSize string
		wantErr    bool
		check      func(t *testing.T, args []string)
	}{
		{
			name:       "striped with stripe size",
			lvmType:    stripedType,
			pvs:        2,
			stripeSize: "64k",
			check: func(t *testing.T, args []string) {
				if !hasFlagValue(args, "--type", "striped") {
					t.Errorf("expected --type striped, got %v", args)
				}
				if !hasFlagValue(args, "--stripes", "2") {
					t.Errorf("expected --stripes 2, got %v", args)
				}
				if !hasFlagValue(args, "--stripesize", "64k") {
					t.Errorf("expected --stripesize 64k, got %v", args)
				}
			},
		},
		{
			name:    "striped without stripe size",
			lvmType: stripedType,
			pvs:     2,
			check: func(t *testing.T, args []string) {
				if !hasFlagValue(args, "--type", "striped") {
					t.Errorf("expected --type striped, got %v", args)
				}
				if hasFlag(args, "--stripesize") {
					t.Errorf("did not expect --stripesize, got %v", args)
				}
			},
		},
		{
			name:       "striped stripe size is trimmed",
			lvmType:    stripedType,
			pvs:        3,
			stripeSize: "  256k  ",
			check: func(t *testing.T, args []string) {
				if !hasFlagValue(args, "--stripesize", "256k") {
					t.Errorf("expected trimmed --stripesize 256k, got %v", args)
				}
			},
		},
		{
			name:       "striped falls back to linear with a single pv",
			lvmType:    stripedType,
			pvs:        1,
			stripeSize: "64k",
			check: func(t *testing.T, args []string) {
				if hasFlag(args, "--type") {
					t.Errorf("did not expect --type for linear fallback, got %v", args)
				}
				if hasFlag(args, "--stripes") {
					t.Errorf("did not expect --stripes for linear fallback, got %v", args)
				}
				if hasFlag(args, "--stripesize") {
					t.Errorf("did not expect --stripesize for linear fallback, got %v", args)
				}
			},
		},
		{
			name:       "stripe size ignored for linear",
			lvmType:    linearType,
			pvs:        2,
			stripeSize: "64k",
			check: func(t *testing.T, args []string) {
				if hasFlag(args, "--stripesize") {
					t.Errorf("did not expect --stripesize for linear, got %v", args)
				}
			},
		},
		{
			name:       "stripe size ignored for mirror",
			lvmType:    mirrorType,
			pvs:        2,
			stripeSize: "64k",
			check: func(t *testing.T, args []string) {
				if !hasFlagValue(args, "--type", "raid1") {
					t.Errorf("expected --type raid1, got %v", args)
				}
				if hasFlag(args, "--stripesize") {
					t.Errorf("did not expect --stripesize for mirror, got %v", args)
				}
			},
		},
		{
			name:    "unsupported type errors",
			lvmType: "bogus",
			pvs:     2,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := buildLvcreateArgs(name, size, tt.lvmType, tt.pvs, tt.integrity, tt.stripeSize)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got nil (args %v)", args)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !hasFlagValue(args, "-n", name) {
				t.Errorf("expected -n %s, got %v", name, args)
			}
			if tt.check != nil {
				tt.check(t, args)
			}
		})
	}
}

func TestBuildMkfsArgs(t *testing.T) {
	const device = "/dev/csi-lvm/pvc-123"

	tests := []struct {
		name        string
		forceFormat bool
		mkfsOptions string
		check       func(t *testing.T, args []string)
	}{
		{
			name: "no options is just the device path",
			check: func(t *testing.T, args []string) {
				if len(args) != 1 {
					t.Errorf("expected only the device path, got %v", args)
				}
				if hasFlag(args, "-f") {
					t.Errorf("did not expect -f, got %v", args)
				}
			},
		},
		{
			name:        "force only",
			forceFormat: true,
			check: func(t *testing.T, args []string) {
				if args[0] != "-f" {
					t.Errorf("expected -f first, got %v", args)
				}
				if len(args) != 2 {
					t.Errorf("expected -f and the device path only, got %v", args)
				}
			},
		},
		{
			name:        "mkfs options only",
			mkfsOptions: "-E stride=64,stripe_width=384",
			check: func(t *testing.T, args []string) {
				if hasFlag(args, "-f") {
					t.Errorf("did not expect -f, got %v", args)
				}
				if !hasFlagValue(args, "-E", "stride=64,stripe_width=384") {
					t.Errorf("expected -E stride=64,stripe_width=384, got %v", args)
				}
			},
		},
		{
			name:        "force and mkfs options",
			forceFormat: true,
			mkfsOptions: "-E stride=64,stripe_width=384",
			check: func(t *testing.T, args []string) {
				if args[0] != "-f" {
					t.Errorf("expected -f first, got %v", args)
				}
				if !hasFlagValue(args, "-E", "stride=64,stripe_width=384") {
					t.Errorf("expected -E stride=64,stripe_width=384, got %v", args)
				}
			},
		},
		{
			name:        "surrounding and repeated whitespace is collapsed",
			mkfsOptions: "  -b 4096   -E stride=64  ",
			check: func(t *testing.T, args []string) {
				if !hasFlagValue(args, "-b", "4096") {
					t.Errorf("expected -b 4096, got %v", args)
				}
				if !hasFlagValue(args, "-E", "stride=64") {
					t.Errorf("expected -E stride=64, got %v", args)
				}
				for _, a := range args {
					if a == "" {
						t.Errorf("did not expect an empty argument, got %v", args)
					}
				}
			},
		},
		{
			name:        "whitespace-only options are ignored",
			mkfsOptions: "   ",
			check: func(t *testing.T, args []string) {
				if len(args) != 1 {
					t.Errorf("expected only the device path, got %v", args)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildMkfsArgs(device, tt.forceFormat, tt.mkfsOptions)
			if len(args) == 0 || args[len(args)-1] != device {
				t.Fatalf("expected the device path %q to be the last argument, got %v", device, args)
			}
			if tt.check != nil {
				tt.check(t, args)
			}
		})
	}
}
