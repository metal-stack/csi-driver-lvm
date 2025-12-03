package lvm

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/utils/ptr"
)

const (
	linearType  = "linear"
	stripedType = "striped"
	mirrorType  = "mirror"
)

type vgReport struct {
	Report []struct {
		VG []struct {
			VGName string `json:"vg_name"`
			VGFree string `json:"vg_free"`
		} `json:"vg"`
	} `json:"report"`
}

type lsblk struct {
	BlockDevices []struct {
		FSType *string `json:"fstype"`
	} `json:"blockdevices"`
}

func MountLV(log *slog.Logger, lvname, mountPath string, vgName string, fsType string) (string, error) {
	lvPath := fmt.Sprintf("/dev/%s/%s", vgName, lvname)

	formatted := false
	forceFormat := false
	if fsType == "" {
		fsType = "ext4"
	}
	// check for already formatted
	cmd := exec.Command("lsblk", "-J", "-f", lvPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("unable to check if lv %s is already formatted: %w (%s)", lvPath, err, string(out))
	}

	lsblkReport := lsblk{}
	err = json.Unmarshal(out, &lsblkReport)

	if err != nil {
		return "", fmt.Errorf("failed to format lsblk output: %w", err)
	}

	if len(lsblkReport.BlockDevices) != 1 {
		return "", fmt.Errorf("unexpected amount of blockdevices found for lsblk (%d)", len(lsblkReport.BlockDevices))
	}

	switch f := lsblkReport.BlockDevices[0].FSType; f {
	case nil:
		log.Debug("lv not yet formatted", "lv-path", lvPath)
	case ptr.To("xfs_external_log"):
		forceFormat = true
	default:
		log.Debug("lv already formatted", "lv-path", lvPath, "format", *f)
	}

	if !formatted {
		formatArgs := []string{}
		if forceFormat {
			formatArgs = append(formatArgs, "-f")
		}
		formatArgs = append(formatArgs, lvPath)

		log.Debug("formatting with mkfs", "fs-type", fsType, "args", strings.Join(formatArgs, " "))
		cmd = exec.Command(fmt.Sprintf("mkfs.%s", fsType), formatArgs...) //nolint:gosec
		out, err = cmd.CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("unable to format lv:%s err:%w", lvname, err)
		}
	}

	err = os.MkdirAll(mountPath, 0777|os.ModeSetgid)
	if err != nil {
		return string(out), fmt.Errorf("unable to create mount directory for lv:%s err:%w", lvname, err)
	}

	// --make-shared is required that this mount is visible outside this container.
	mountArgs := []string{"--make-shared", "-t", fsType, lvPath, mountPath}
	log.Debug("mounting with mount", "args", strings.Join(mountArgs, " "))
	cmd = exec.Command("mount", mountArgs...)
	out, err = cmd.CombinedOutput()
	if err != nil {
		mountOutput := string(out)
		if !strings.Contains(mountOutput, "already mounted") {
			return string(out), fmt.Errorf("unable to mount %s to %s err:%w output:%s", lvPath, mountPath, err, out)
		}
	}
	err = os.Chmod(mountPath, 0777|os.ModeSetgid)
	if err != nil {
		return "", fmt.Errorf("unable to change permissions of volume mount %s err:%w", mountPath, err)
	}
	log.Debug("mountlv output", "output", out)
	return "", nil
}

func BindMountLV(log *slog.Logger, lvname, mountPath string, vgName string) (string, error) {
	lvPath := fmt.Sprintf("/dev/%s/%s", vgName, lvname)
	_, err := os.Create(mountPath)
	if err != nil {
		return "", fmt.Errorf("unable to create mount directory for lv:%s err:%w", lvname, err)
	}

	// --make-shared is required that this mount is visible outside this container.
	// --bind is required for raw block volumes to make them visible inside the pod.
	mountArgs := []string{"--make-shared", "--bind", lvPath, mountPath}
	log.Debug("bindmountlv command: mount", "args", strings.Join(mountArgs, " "))
	cmd := exec.Command("mount", mountArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		mountOutput := string(out)
		if !strings.Contains(mountOutput, "already mounted") {
			return string(out), fmt.Errorf("unable to mount %s to %s err:%w output:%s", lvPath, mountPath, err, out)
		}
	}
	err = os.Chmod(mountPath, 0777|os.ModeSetgid)
	if err != nil {
		return "", fmt.Errorf("unable to change permissions of volume mount %s err:%w", mountPath, err)
	}
	log.Debug("bindmountlv output", "output", out)
	return "", nil
}

func UmountLV(log *slog.Logger, targetPath string) {
	cmd := exec.Command("umount", "--lazy", "--force", targetPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		//RETURN err ?
		log.Error("unable to umount", "targetPath", targetPath, "output", out, "error", err)
	}
}

// VgExists checks if the given volume group exists
func VgExists(log *slog.Logger, vgname string) bool {
	cmd := exec.Command("vgs", vgname, "--noheadings", "-o", "vg_name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Debug("unable to list existing volumegroups", "error", err)
		return false
	}
	return vgname == strings.TrimSpace(string(out))
}

// VgActivate execute vgchange -ay to activate all volumes of the volume group
func VgActivate(log *slog.Logger) {
	// scan for vgs and activate if any
	cmd := exec.Command("vgscan")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Debug("unable to scan for volumegroups", "output", out, "error", err)
	}
	cmd = exec.Command("vgchange", "-ay")
	_, err = cmd.CombinedOutput()
	if err != nil {
		log.Debug("unable to activate volumegroups", "output", out, "error", err)
	}
}

func devices(log *slog.Logger, devicesPattern []string) (devices []string, err error) {
	for _, devicePattern := range devicesPattern {
		log.Debug("search devices", "pattern", devicePattern)
		matches, err := filepath.Glob(strings.TrimSpace(devicePattern))
		if err != nil {
			return nil, err
		}
		log.Debug("found devices", "matches", matches)
		devices = append(devices, matches...)
	}
	return devices, nil
}

// CreateVG creates a volume group matching the given device patterns
func CreateVG(log *slog.Logger, name string, devicesPattern string) (string, error) {
	dp := strings.Split(devicesPattern, ",")
	if len(dp) == 0 {
		return name, fmt.Errorf("invalid empty flag %v", dp)
	}

	vgexists := VgExists(log, name)
	if vgexists {
		log.Info("volumegroup already exists", "name", name)
		return name, nil
	}
	VgActivate(log)
	// now check again for existing vg again
	vgexists = VgExists(log, name)
	if vgexists {
		log.Info("volumegroup already exists", "name", name)
		return name, nil
	}

	physicalVolumes, err := devices(log, dp)
	if err != nil {
		return "", fmt.Errorf("unable to lookup devices from devicesPattern %s, err:%w", devicesPattern, err)
	}
	tags := []string{"vg.metal-stack.io/csi-lvm-driver"}

	args := []string{"-v", name}
	args = append(args, physicalVolumes...)
	for _, tag := range tags {
		args = append(args, "--addtag", tag)
	}
	log.Debug("creating volumegroup", "name", name, "devices", physicalVolumes)
	cmd := exec.Command("vgcreate", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// CreateLV creates the new volume
// used by lvcreate provisioner pod and by nodeserver for ephemeral volumes
func CreateLV(log *slog.Logger, vg string, name string, size uint64, lvmType string, integrity bool) (string, error) {
	if LvExists(log, vg, name) {
		log.Debug("logicalvolume already exists", "name", name)
		return name, nil
	}

	if size == 0 {
		return "", fmt.Errorf("size must be greater than 0")
	}

	switch lvmType {
	case "linear", "mirror", "striped":
		// These are supported lvm types
	default:
		return "", fmt.Errorf("lvmType is incorrect: %s", lvmType)
	}

	args := []string{"-v", "--yes", "-n", name, "-W", "y", "-L", fmt.Sprintf("%db", size)}

	pvs, err := pvCount(vg)
	if err != nil {
		return "", fmt.Errorf("unable to determine pv count of vg: %w", err)
	}

	if pvs < 2 {
		log.Warn("pvcount is <2 only linear is supported")
		lvmType = linearType
	}

	switch lvmType {
	case stripedType:
		args = append(args, "--type", "striped", "--stripes", fmt.Sprintf("%d", pvs))
	case mirrorType:
		args = append(args, "--type", "raid1", "--mirrors", "1", "--nosync")
	case linearType:
	default:
		return "", fmt.Errorf("unsupported lvmtype: %s", lvmType)
	}

	if integrity {
		switch lvmType {
		case mirrorType:
			args = append(args, "--raidintegrity", "y")
		default:
			return "", fmt.Errorf("integrity is only supported if type is mirror")
		}
	}

	tags := []string{"lv.metal-stack.io/csi-lvm-driver"}
	for _, tag := range tags {
		args = append(args, "--addtag", tag)
	}
	args = append(args, vg)
	log.Debug("lvcreate", "args", args)
	cmd := exec.Command("lvcreate", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func LvExists(log *slog.Logger, vg string, name string) bool {
	var (
		vgname = vg + "/" + name
		cmd    = exec.Command("lvs", vgname, "--noheadings", "-o", "lv_name")
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("unable to list existing volumes", "error", err)
		return false
	}

	return name == strings.TrimSpace(string(out))
}

func ExtendLVS(log *slog.Logger, vg string, name string, size uint64, isBlock bool) (string, error) {
	if !LvExists(log, vg, name) {
		return "", fmt.Errorf("logical volume %s does not exist", name)
	}

	args := []string{"-L", fmt.Sprintf("%db", size)}
	if isBlock {
		args = append(args, "-n")
	} else {
		args = append(args, "-r")
	}
	args = append(args, fmt.Sprintf("%s/%s", vg, name))

	log.Debug("lvextend", "args", args)

	cmd := exec.Command("lvextend", args...)
	out, err := cmd.CombinedOutput()

	return string(out), err
}

// RemoveLVS executes lvremove
func RemoveLVS(log *slog.Logger, vg string, name string) (string, error) {
	if !LvExists(log, vg, name) {
		return fmt.Sprintf("logical volume %s does not exist. Assuming it has already been deleted.", name), nil
	}

	args := []string{"-q", "-y"}
	args = append(args, fmt.Sprintf("%s/%s", vg, name))

	log.Debug("lvremove", "args", args)

	cmd := exec.Command("lvremove", args...)
	out, err := cmd.CombinedOutput()

	return string(out), err
}

func pvCount(vgname string) (int, error) {
	cmd := exec.Command("vgs", vgname, "--noheadings", "-o", "pv_count")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, err
	}

	outStr := strings.TrimSpace(string(out))
	count, err := strconv.Atoi(outStr)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func VgStats(log *slog.Logger, vgName string) (int64, error) {
	args := []string{vgName, "--units", "B", "--nosuffix", "--reportformat", "json"}
	log.Debug("getting stats of vg", "vg-name", vgName, "args", strings.Join(args, " "))

	cmd := exec.Command("vgs", args...) //nolint:gosec
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("unable to get vg stats of %s: %w", vgName, err)
	}

	pvReport := vgReport{}
	err = json.Unmarshal(out, &pvReport)
	if err != nil {
		return 0, fmt.Errorf("failed to format vgs output: %w", err)
	}

	for _, report := range pvReport.Report {
		for _, vg := range report.VG {
			if vg.VGName != vgName {
				continue
			}

			free, err := strconv.ParseInt(vg.VGFree, 10, 0)
			if err != nil {
				return 0, fmt.Errorf("failed to parse free space for device %s with error: %w", vg.VGName, err)
			}

			return free, nil
		}
	}

	return 0, fmt.Errorf("failed to find the free space for device %s", vgName)
}
