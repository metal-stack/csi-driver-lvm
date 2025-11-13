package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path"

	"github.com/metal-stack/csi-driver-lvm/pkg/server"
)

func init() {
	err := flag.Set("logtostderr", "true")
	if err != nil {
		log.Printf("unable to configure logging to stdout:%v\n", err)
	}
}

var (
	endpoint          = flag.String("endpoint", "unix://tmp/csi.sock", "CSI endpoint")
	hostWritePath     = flag.String("hostwritepath", "/etc/lvm", "host path where config, cache & backups will be written to")
	driverName        = flag.String("drivername", "lvm.csi.metal-stack.io", "name of the driver")
	nodeID            = flag.String("nodeid", "", "node id")
	ephemeral         = flag.Bool("ephemeral", false, "publish volumes in ephemeral mode even if kubelet did not ask for it (only needed for Kubernetes 1.15)")
	maxVolumesPerNode = flag.Int64("maxvolumespernode", 0, "limit of volumes per node")
	showVersion       = flag.Bool("version", false, "Show version.")
	devicesPattern    = flag.String("devices", "", "comma-separated grok patterns of the physical volumes to use.")
	vgName            = flag.String("vgname", "csi-lvm", "name of volume group")

	// Set by the build process
	version = ""
)

func main() {
	flag.Parse()

	if *showVersion {
		baseName := path.Base(os.Args[0])
		fmt.Println(baseName, version)
		return
	}

	if *ephemeral {
		fmt.Fprintln(os.Stderr, "Deprecation warning: The ephemeral flag is deprecated and should only be used when deploying on Kubernetes 1.15. It will be removed in the future.")
	}

	handle()
	os.Exit(0)
}

func handle() {
	driver, err := server.NewDriver(*driverName, *nodeID, *endpoint, *hostWritePath, *ephemeral, *maxVolumesPerNode, version, *devicesPattern, *vgName)
	if err != nil {
		fmt.Printf("Failed to initialize driver: %s\n", err.Error())
		os.Exit(1)
	}
	driver.Run()
}
