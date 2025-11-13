package lvm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

const (
	linearType         = "linear"
	stripedType        = "striped"
	mirrorType         = "mirror"
	fsTypeRegexpString = `TYPE="(\w+)"`
)

var (
	fsTypeRegexp = regexp.MustCompile(fsTypeRegexpString)
)

func MountLV(lvname, mountPath string, vgName string, fsType string) (string, error) {
	lvPath := fmt.Sprintf("/dev/%s/%s", vgName, lvname)

	formatted := false
	forceFormat := false
	if fsType == "" {
		fsType = "ext4"
	}
	// check for already formatted
	cmd := exec.Command("blkid", lvPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to check if %s is already formatted:%v", lvPath, err)
	}
	matches := fsTypeRegexp.FindStringSubmatch(string(out))
	if len(matches) > 1 {
		if matches[1] == "xfs_external_log" { // If old xfs signature was found
			forceFormat = true
		} else {
			if matches[1] != fsType {
				return string(out), fmt.Errorf("target fsType is %s but %s found", fsType, matches[1])
			}

			formatted = true
		}
	}

	if !formatted {
		formatArgs := []string{}
		if forceFormat {
			formatArgs = append(formatArgs, "-f")
		}
		formatArgs = append(formatArgs, lvPath)

		klog.Infof("formatting with mkfs.%s %s", fsType, strings.Join(formatArgs, " "))
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
	klog.Infof("mountlv command: mount %s", mountArgs)
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
	klog.Infof("mountlv output:%s", out)
	return "", nil
}

func BindMountLV(lvname, mountPath string, vgName string) (string, error) {
	lvPath := fmt.Sprintf("/dev/%s/%s", vgName, lvname)
	_, err := os.Create(mountPath)
	if err != nil {
		return "", fmt.Errorf("unable to create mount directory for lv:%s err:%w", lvname, err)
	}

	// --make-shared is required that this mount is visible outside this container.
	// --bind is required for raw block volumes to make them visible inside the pod.
	mountArgs := []string{"--make-shared", "--bind", lvPath, mountPath}
	klog.Infof("bindmountlv command: mount %s", mountArgs)
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
	klog.Infof("bindmountlv output:%s", out)
	return "", nil
}

func UmountLV(targetPath string) {
	cmd := exec.Command("umount", "--lazy", "--force", targetPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Errorf("unable to umount %s output:%s err:%v", targetPath, string(out), err)
	}
}

// VgExists checks if the given volume group exists
func vgExists(vgname string) bool {
	cmd := exec.Command("vgs", vgname, "--noheadings", "-o", "vg_name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to list existing volumegroups:%v", err)
		return false
	}
	return vgname == strings.TrimSpace(string(out))
}

// VgActivate execute vgchange -ay to activate all volumes of the volume group
func vgActivate() {
	// scan for vgs and activate if any
	cmd := exec.Command("vgscan")
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to scan for volumegroups:%s %v", out, err)
	}
	cmd = exec.Command("vgchange", "-ay")
	_, err = cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to activate volumegroups:%s %v", out, err)
	}
}

func devices(devicesPattern []string) (devices []string, err error) {
	for _, devicePattern := range devicesPattern {
		klog.Infof("search devices: %s ", devicePattern)
		matches, err := filepath.Glob(strings.TrimSpace(devicePattern))
		if err != nil {
			return nil, err
		}
		klog.Infof("found: %s", matches)
		devices = append(devices, matches...)
	}
	return devices, nil
}

// CreateVG creates a volume group matching the given device patterns
func CreateVG(name string, devicesPattern string) (string, error) {
	dp := strings.Split(devicesPattern, ",")
	if len(dp) == 0 {
		return name, fmt.Errorf("invalid empty flag %v", dp)
	}

	vgexists := vgExists(name)
	if vgexists {
		klog.Infof("volumegroup: %s already exists\n", name)
		return name, nil
	}
	vgActivate()
	// now check again for existing vg again
	vgexists = vgExists(name)
	if vgexists {
		klog.Infof("volumegroup: %s already exists\n", name)
		return name, nil
	}

	physicalVolumes, err := devices(dp)
	if err != nil {
		return "", fmt.Errorf("unable to lookup devices from devicesPattern %s, err:%w", devicesPattern, err)
	}
	tags := []string{"vg.metal-stack.io/csi-lvm-driver"}

	args := []string{"-v", name}
	args = append(args, physicalVolumes...)
	for _, tag := range tags {
		args = append(args, "--addtag", tag)
	}
	klog.Infof("create vg with command: vgcreate %v", args)
	cmd := exec.Command("vgcreate", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// CreateLVS creates the new volume
// used by lvcreate provisioner pod and by nodeserver for ephemeral volumes
func CreateLVS(vg string, name string, size uint64, lvmType string, integrity bool) (string, error) {

	if LvExists(vg, name) {
		klog.Infof("logicalvolume: %s already exists\n", name)
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

	// TODO: check available capacity, fail if request doesn't fit

	args := []string{"-v", "--yes", "-n", name, "-W", "y", "-L", fmt.Sprintf("%db", size)}

	pvs, err := pvCount(vg)
	if err != nil {
		return "", fmt.Errorf("unable to determine pv count of vg: %w", err)
	}

	if pvs < 2 {
		klog.Warning("pvcount is <2 only linear is supported")
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
	klog.Infof("lvcreate %s", args)
	cmd := exec.Command("lvcreate", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func LvExists(vg string, name string) bool {
	vgname := vg + "/" + name
	cmd := exec.Command("lvs", vgname, "--noheadings", "-o", "lv_name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to list existing volumes:%v", err)
		return false
	}
	return name == strings.TrimSpace(string(out))
}

func ExtendLVS(vg string, name string, size uint64, isBlock bool) (string, error) {
	if !LvExists(vg, name) {
		return "", fmt.Errorf("logical volume %s does not exist", name)
	}

	// TODO: check available capacity, fail if request doesn't fit

	args := []string{"-L", fmt.Sprintf("%db", size)}
	if isBlock {
		args = append(args, "-n")
	} else {
		args = append(args, "-r")
	}
	args = append(args, fmt.Sprintf("%s/%s", vg, name))
	klog.Infof("lvextend %s", args)
	cmd := exec.Command("lvextend", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RemoveLVS executes lvremove
func RemoveLVS(vg string, name string) (string, error) {
	if !LvExists(vg, name) {
		return fmt.Sprintf("logical volume %s does not exist. Assuming it has already been deleted.", name), nil
	}
	args := []string{"-q", "-y"}
	args = append(args, fmt.Sprintf("%s/%s", vg, name))
	klog.Infof("lvremove %s", args)
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
