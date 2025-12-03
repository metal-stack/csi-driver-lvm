package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path"

	"github.com/metal-stack/csi-driver-lvm/pkg/server"
)

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
	logLevel          = flag.String("log-level", "info", "log-level of the application")

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
	var lvlvar slog.LevelVar
	err := lvlvar.UnmarshalText([]byte(*logLevel))
	if err != nil {
		panic("not able to determine log-level")
	}
	log := slog.New(
		slog.NewJSONHandler(
			os.Stdout,
			&slog.HandlerOptions{
				Level: lvlvar.Level(),
			},
		),
	)

	driver, err := server.NewDriver(log, *driverName, *nodeID, *endpoint, *hostWritePath, *ephemeral, *maxVolumesPerNode, version, *devicesPattern, *vgName)
	if err != nil {
		log.Error("failed to initialize driver", "error", err)
		os.Exit(1)
	}
	driver.Run()
}
