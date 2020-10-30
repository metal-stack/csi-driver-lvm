package main

import (
	"fmt"

	lvm "github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"github.com/urfave/cli/v2"
	"k8s.io/klog/v2"
)

func restoreSnapshotCmd() *cli.Command {
	return &cli.Command{
		Name: "restoresnapshot",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  flagLVName,
				Usage: "Required. Specify lv name.",
			},
			&cli.StringFlag{
				Name:  flagSnapshotName,
				Usage: "Required. Specify snapshot name.",
			},
			&cli.StringFlag{
				Name:  flagVGName,
				Usage: "Required. the name of the volumegroup",
			},
			&cli.StringFlag{
				Name:  flagS3Parameter,
				Usage: "Required. S3 parameter as base64 encoded json.",
			},
		},
		Action: func(c *cli.Context) error {
			if err := restoreSnapshot(c); err != nil {
				klog.Fatalf("Error creating snapshot: %v", err)
				return err
			}
			return nil
		},
	}
}

func restoreSnapshot(c *cli.Context) error {
	lvName := c.String(flagLVName)
	if lvName == "" {
		return fmt.Errorf("invalid empty flag %v", flagLVName)
	}
	vgName := c.String(flagVGName)
	if vgName == "" {
		return fmt.Errorf("invalid empty flag %v", flagVGName)
	}
	snapshotName := c.String(flagSnapshotName)
	if snapshotName == "" {
		return fmt.Errorf("invalid empty flag %v", flagSnapshotName)
	}
	s3parameterString := c.String(flagS3Parameter)
	if s3parameterString == "" {
		return fmt.Errorf("invalid empty flag %v", flagS3Parameter)
	}
	s3parameter, err := lvm.DecodeS3Parameter(s3parameterString)
	if err != nil {
		return fmt.Errorf("unable to decode %s", flagS3Parameter)
	}

	klog.Infof("restore %s from snapshot %s", lvName, snapshotName)

	output, err := lvm.RestoreS3Snapshot(vgName, lvName, snapshotName, s3parameter)
	if err != nil {
		return fmt.Errorf("unable to create snapshot: %v output:%s", err, output)
	}

	return nil
}
