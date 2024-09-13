package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
	"k8s.io/klog/v2"
)

const (
	flagLVName         = "lvname"
	flagLVSize         = "lvsize"
	flagVGName         = "vgname"
	flagDevicesPattern = "devices"
	flagLVMType        = "lvmtype"
	flagIntegrity      = "integrity"
)

func cmdNotFound(c *cli.Context, command string) {
	panic(fmt.Errorf("unrecognized command: %s", command))
}

func onUsageError(c *cli.Context, err error, isSubcommand bool) error {
	panic(fmt.Errorf("usage error, please check your command"))
}

func main() {
	p := cli.NewApp()
	p.Usage = "LVM Provisioner Pod"
	p.Commands = []*cli.Command{
		createLVCmd(),
		deleteLVCmd(),
	}
	p.CommandNotFound = cmdNotFound
	p.OnUsageError = onUsageError

	klog.Infof("starting csi-lvmplugin-provisioner")

	if err := p.Run(os.Args); err != nil {
		klog.Fatalf("Critical error: %v", err)
	}
}
