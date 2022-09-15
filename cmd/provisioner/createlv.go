package main

import (
	"fmt"

	lvm "github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"github.com/urfave/cli/v2"
	"k8s.io/klog/v2"
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
			&cli.StringFlag{
				Name:  flagDevicesPattern,
				Usage: "Required. comma-separated grok patterns of the physical volumes to use.",
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
	lvmType := c.String(flagLVMType)
	if lvmType == "" {
		return fmt.Errorf("invalid empty flag %v", flagLVMType)
	}
	devicesPattern := c.String(flagDevicesPattern)
	if devicesPattern == "" {
		return fmt.Errorf("invalid empty flag %v", flagDevicesPattern)
	}

	klog.Infof("create lv %s size:%d vg:%s devicespattern:%s  type:%s", lvName, lvSize, vgName, devicesPattern, lvmType)

	output, err := lvm.CreateVG(vgName, devicesPattern)
	if err != nil {
		return fmt.Errorf("unable to create vg: %w output:%s", err, output)
	}

	output, err = lvm.CreateLVS(vgName, lvName, lvSize, lvmType)
	if err != nil {
		return fmt.Errorf("unable to create lv: %w output:%s", err, output)
	}
	return nil
}
