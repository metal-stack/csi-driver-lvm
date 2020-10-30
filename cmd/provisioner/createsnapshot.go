package main

import (
	"fmt"

	lvm "github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"github.com/urfave/cli/v2"
	"k8s.io/klog/v2"
)

func createSnapshotCmd() *cli.Command {
	return &cli.Command{
		Name: "createsnapshot",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  flagLVName,
				Usage: "Required. Specify lv name.",
			},
			&cli.StringFlag{
				Name:  flagSnapshotName,
				Usage: "Required. Specify snapshot name.",
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
				Name:  flagS3Parameter,
				Usage: "Required. S3 parameter as base64 encoded json.",
			},
			&cli.IntFlag{
				Name:  flagLvmSnapshotBufferPercentage,
				Usage: "Required. Amount (in percent) to use for lvm snapshots during creation.",
			},

		},
		Action: func(c *cli.Context) error {
			if err := createSnapshot(c); err != nil {
				klog.Fatalf("Error creating snapshot: %v", err)
				return err
			}
			return nil
		},
	}
}

func createSnapshot(c *cli.Context) error {
	lvName := c.String(flagLVName)
	if lvName == "" {
		return fmt.Errorf("invalid empty flag %v", flagLVName)
	}
	vgName := c.String(flagVGName)
	if vgName == "" {
		return fmt.Errorf("invalid empty flag %v", flagVGName)
	}
	lvSize := c.Uint64(flagLVSize)
	if lvSize == 0 {
		return fmt.Errorf("invalid empty flag %v", flagLVSize)
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
	lvmSnapshotBufferPercentage := c.Int(flagLvmSnapshotBufferPercentage)
	if lvSize == 0 {
		return fmt.Errorf("invalid empty flag %v", flagLvmSnapshotBufferPercentage)
	}

	// create lvm snapshot

	klog.Infof("create snapshot %s from %s", snapshotName, lvName)

	output, err := lvm.CreateS3Snapshot(vgName, lvName, snapshotName, lvSize, s3parameter, lvmSnapshotBufferPercentage)
	if err != nil {
		return fmt.Errorf("unable to create snapshot: %v output:%s", err, output)
	}

	return nil
}
