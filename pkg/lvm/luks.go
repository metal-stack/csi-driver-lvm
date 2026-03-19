package lvm

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"

	"golang.org/x/sys/cpu"
)

const (
	cryptsetupCmd       = "cryptsetup"
	diskMapperPath      = "/dev/mapper/"
	defaultLuksType     = "luks2"
	defaultLuksHash     = "sha256"
	defaultLuksCipher   = "aes-xts-plain64"
	defaultLuksKeySize  = "256"
	defaultPbkdfMemSize = "65535"
	luksMapperPrefix    = "csi-lvm-"
)

// LuksFormat formats the device with LUKS2 encryption using the given passphrase.
func LuksFormat(log *slog.Logger, devicePath, passphrase string) error {
	args := []string{
		"-q",
		"--type", defaultLuksType,
		"--hash", defaultLuksHash,
		"--cipher", defaultLuksCipher,
		"--key-size", defaultLuksKeySize,
		"--key-file", os.Stdin.Name(),
		"--pbkdf-memory", defaultPbkdfMemSize,
		"luksFormat", devicePath,
	}

	log.Info("formatting device with LUKS", "device", devicePath)

	cmd := exec.Command(cryptsetupCmd, args...)
	cmd.Stdin = strings.NewReader(passphrase)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unable to luksFormat device %s: %w (%s)", devicePath, err, string(out))
	}

	return nil
}

// LuksOpen opens a LUKS device and maps it to the given mapper name.
func LuksOpen(log *slog.Logger, devicePath, mapperName, passphrase string) error {
	args := []string{
		"luksOpen", devicePath, mapperName,
		"--disable-keyring", // LUKS2 volumes require passphrase on resize if keyring is not disabled on open
		"--key-file", os.Stdin.Name(),
		"--perf-same_cpu_crypt",
		"--perf-submit_from_crypt_cpus",
		"--perf-no_read_workqueue",
		"--perf-no_write_workqueue",
	}

	log.Info("opening LUKS device", "device", devicePath, "mapper", mapperName)

	cmd := exec.Command(cryptsetupCmd, args...)
	cmd.Stdin = strings.NewReader(passphrase)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unable to luksOpen device %s: %w (%s)", devicePath, err, string(out))
	}

	return nil
}

// LuksClose closes the LUKS device with the given mapper name.
func LuksClose(log *slog.Logger, mapperName string) error {
	mapperPath := path.Join(diskMapperPath, mapperName)

	log.Info("closing LUKS device", "mapper", mapperPath)

	cmd := exec.Command(cryptsetupCmd, "luksClose", mapperPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unable to luksClose %s: %w (%s)", mapperPath, err, string(out))
	}

	return nil
}

// LuksResize resizes the LUKS device with the given mapper name.
func LuksResize(log *slog.Logger, mapperName string) error {
	mapperPath := path.Join(diskMapperPath, mapperName)

	log.Info("resizing LUKS device", "mapper", mapperPath)

	cmd := exec.Command(cryptsetupCmd, "resize", mapperPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unable to resize LUKS device %s: %w (%s)", mapperPath, err, string(out))
	}

	return nil
}

// LuksStatus returns true if the LUKS device with the given mapper name is active.
func LuksStatus(log *slog.Logger, mapperName string) bool {
	mapperPath := path.Join(diskMapperPath, mapperName)

	cmd := exec.Command(cryptsetupCmd, "status", mapperPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Debug("LUKS status check failed", "mapper", mapperPath, "error", err, "output", string(out))
		return false
	}

	return strings.Contains(string(out), "is active")
}

// IsLuks returns true if the device at devicePath is a LUKS-formatted device.
func IsLuks(log *slog.Logger, devicePath string) (bool, error) {
	cmd := exec.Command(cryptsetupCmd, "isLuks", devicePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 1 {
				return false, nil
			}
		}
		return false, fmt.Errorf("unable to check if device %s is LUKS: %w (%s)", devicePath, err, string(out))
	}

	return true, nil
}

// LVDevicePath returns the device path for a logical volume.
func LVDevicePath(log *slog.Logger, vgName, lvName string) (string, error) {
	devicePath := fmt.Sprintf("/dev/%s/%s", vgName, lvName)

	if _, err := os.Stat(devicePath); err != nil {
		return "", fmt.Errorf("device path %s does not exist for %s/%s: %w", devicePath, vgName, lvName, err)
	}

	log.Debug("resolved LV device path", "vg", vgName, "lv", lvName, "path", devicePath)
	return devicePath, nil
}

// EncryptedDevicePath returns the mapper device path if the LUKS device is active,
// or an empty string if it is not active.
func EncryptedDevicePath(log *slog.Logger, mapperName string) (string, error) {
	mapperPath := path.Join(diskMapperPath, mapperName)

	_, err := os.Stat(mapperPath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("unable to stat mapper path %s: %w", mapperPath, err)
	}

	if !LuksStatus(log, mapperName) {
		return "", nil
	}

	return mapperPath, nil
}

// LuksMapperName returns a deterministic mapper name for the given volume ID.
func LuksMapperName(volumeID string) string {
	return luksMapperPrefix + volumeID
}

// IsAESSupported checks if the CPU supports AES instructions.
func IsAESSupported() bool {
	switch runtime.GOARCH {
	case "arm64":
		return cpu.ARM64.HasAES
	case "amd64":
		return cpu.X86.HasAES
	case "arm":
		return cpu.ARM.HasAES
	default:
		return false
	}
}
