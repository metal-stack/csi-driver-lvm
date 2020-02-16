package main

import (
	"context"
	"fmt"

	"github.com/google/lvmd/commands"
	"github.com/urfave/cli/v2"
	"k8s.io/klog"
)

func deleteLVCmd() *cli.Command {
	return &cli.Command{
		Name: "deletelv",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  flagLVName,
				Usage: "Required. Specify lv name.",
			},
			&cli.StringFlag{
				Name:  flagVGName,
				Usage: "Required. the name of the volumegroup",
			},
		},
		Action: func(c *cli.Context) error {
			if err := deleteLV(c); err != nil {
				klog.Fatalf("Error deleting lv: %v", err)
				return err
			}
			return nil
		},
	}
}

func deleteLV(c *cli.Context) error {
	lvName := c.String(flagLVName)
	if lvName == "" {
		return fmt.Errorf("invalid empty flag %v", flagLVName)
	}
	vgName := c.String(flagVGName)
	if vgName == "" {
		return fmt.Errorf("invalid empty flag %v", flagVGName)
	}

	klog.Infof("delete lv %s vg:%s ", lvName, vgName)
	/*
		output, err := umountLV(lvName, vgName, dirName)
		if err != nil {
			return fmt.Errorf("unable to delete lv: %v output:%s", err, output)
		}
	*/
	output, err := commands.RemoveLV(context.Background(), vgName, lvName)
	if err != nil {
		return fmt.Errorf("unable to delete lv: %v output:%s", err, output)
	}
	klog.Infof("lv %s vg:%s deleted", lvName, vgName)
	return nil
}
