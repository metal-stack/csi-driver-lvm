package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/lvmd/commands"
	lvm "github.com/mwennrich/csi-driver-lvm/pkg/lvm"
	"github.com/urfave/cli/v2"
	"k8s.io/klog"
)

const (
	linearType  = "linear"
	stripedType = "striped"
	mirrorType  = "mirror"
)

func createLVCmd() *cli.Command {
	return &cli.Command{
		Name: "createlv",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  flagLVName,
				Usage: "Required. Specify lv name.",
			},
			&cli.Uint64Flag{
				Name:  flagLVSize,
				Usage: "Required. The size of the lv in MiB",
			},
			&cli.StringFlag{
				Name:  flagVGName,
				Usage: "Required. the name of the volumegroup",
			},
			&cli.StringFlag{
				Name:  flagLVMType,
				Usage: "Required. type of lvs, can be either striped or mirrored",
			},
			&cli.StringSliceFlag{
				Name:  flagDevicesPattern,
				Usage: "Required. the patterns of the physical volumes to use.",
			},
			&cli.BoolFlag{
				Name:  flagBlockMode,
				Usage: "Optional. create a block device only, default false",
			},
		},
		Action: func(c *cli.Context) error {
			if err := createLV(c); err != nil {
				klog.Fatalf("Error creating lv: %v", err)
				return err
			}
			return nil
		},
	}
}

func createLV(c *cli.Context) error {
	lvName := c.String(flagLVName)
	if lvName == "" {
		return fmt.Errorf("invalid empty flag %v", flagLVName)
	}
	lvSize := c.Uint64(flagLVSize)
	if lvSize == 0 {
		return fmt.Errorf("invalid empty flag %v", flagLVSize)
	}
	vgName := c.String(flagVGName)
	if vgName == "" {
		return fmt.Errorf("invalid empty flag %v", flagVGName)
	}
	devicesPattern := c.StringSlice(flagDevicesPattern)
	if len(devicesPattern) == 0 {
		return fmt.Errorf("invalid empty flag %v", flagDevicesPattern)
	}
	lvmType := c.String(flagLVMType)
	if lvmType == "" {
		return fmt.Errorf("invalid empty flag %v", flagLVMType)
	}
	blockMode := c.Bool(flagBlockMode)

	klog.Infof("create lv %s size:%d vg:%s devicespattern:%s  type:%s block:%t", lvName, lvSize, vgName, devicesPattern, lvmType, blockMode)

	// TODO
	// createVG could get called once at the start of the nodeserver
	output, err := createVG(vgName, devicesPattern)
	if err != nil {
		return fmt.Errorf("unable to create vg: %v output:%s", err, output)
	}

	output, err = createLVS(context.Background(), vgName, lvName, lvSize, lvmType, blockMode)
	if err != nil {
		return fmt.Errorf("unable to create lv: %v output:%s", err, output)
	}
	return nil
}

// TODO
// move everything below to lvm package
// ephemeral volumes can be created directly on the node without a provisioner pod,
// so these functions are needed there too anyway

func devices(devicesPattern []string) (devices []string, err error) {
	for _, devicePattern := range devicesPattern {
		klog.Infof("search devices :%s ", devicePattern)
		matches, err := filepath.Glob(devicePattern)
		if err != nil {
			return nil, err
		}
		klog.Infof("found: %s", matches)
		devices = append(devices, matches...)
	}
	return devices, nil
}

func createVG(name string, devicesPattern []string) (string, error) {
	vgexists := lvm.VgExists(name)
	if vgexists {
		klog.Infof("volumegroup: %s already exists\n", name)
		return name, nil
	}
	lvm.VgActivate(name)
	// now check again for existing vg again
	vgexists = lvm.VgExists(name)
	if vgexists {
		klog.Infof("volumegroup: %s already exists\n", name)
		return name, nil
	}

	physicalVolumes, err := devices(devicesPattern)
	if err != nil {
		return "", fmt.Errorf("unable to lookup devices from devicesPattern %s, err:%v", devicesPattern, err)
	}
	tags := []string{"vg.metal-stack.io/csi-lvm"}

	args := []string{"-v", name}
	args = append(args, physicalVolumes...)
	for _, tag := range tags {
		args = append(args, "--add-tag", tag)
	}
	klog.Infof("create vg with command: vgcreate %v", args)
	cmd := exec.Command("vgcreate", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// createLV creates a new volume
func createLVS(ctx context.Context, vg string, name string, size uint64, lvmType string, blockMode bool) (string, error) {
	lvs, err := commands.ListLV(context.Background(), vg+"/"+name)
	if err != nil {
		klog.Infof("unable to list existing logicalvolumes:%v", err)
	}
	lvExists := false
	for _, lv := range lvs {
		klog.Infof("compare lv:%s with:%s\n", lv.Name, name)
		if strings.Contains(lv.Name, name) {
			lvExists = true
			break
		}
	}

	if lvExists {
		klog.Infof("logicalvolume: %s already exists\n", name)
		return name, nil
	}

	if size == 0 {
		return "", fmt.Errorf("size must be greater than 0")
	}

	args := []string{"-v", "-n", name, "-W", "y", "-L", fmt.Sprintf("%db", size)}

	pvs, err := pvCount(vg)
	if err != nil {
		return "", fmt.Errorf("unable to determine pv count of vg: %v", err)
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

	// TODO
	// isBlock tags are not needed with the csi driver
	tags := []string{"lv.metal-stack.io/csi-lvm", "isBlock=" + strconv.FormatBool(blockMode)}
	for _, tag := range tags {
		args = append(args, "--add-tag", tag)
	}
	args = append(args, vg)
	klog.Infof("lvreate %s", args)
	cmd := exec.Command("lvcreate", args...)
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
