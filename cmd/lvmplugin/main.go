package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path"

	v1alpha1 "github.com/metal-stack/csi-driver-lvm/api/v1alpha1"
	"github.com/metal-stack/csi-driver-lvm/pkg/nodeagent"
	"github.com/metal-stack/csi-driver-lvm/pkg/server"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	enableDRBD        = flag.Bool("enable-drbd", false, "enable DRBD replication support")

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
		slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})).Error("not able to determine log-level", "error", err)
		os.Exit(1)
	}

	log := slog.New(
		slog.NewJSONHandler(
			os.Stdout,
			&slog.HandlerOptions{
				Level: lvlvar.Level(),
			},
		),
	).With("node", *nodeID)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var k8sClient client.Client
	if *enableDRBD {
		k8sClient, err = createK8sClient(log)
		if err != nil {
			log.Error("failed to create k8s client for drbd support", "error", err)
			os.Exit(1)
		}
		log.Info("drbd support enabled, starting node agent")
		agent := nodeagent.New(log, k8sClient, *nodeID, *vgName)
		go agent.Run(ctx)
	}

	driver, err := server.NewDriver(log, *driverName, *nodeID, *endpoint, *hostWritePath, *ephemeral, *maxVolumesPerNode, version, *devicesPattern, *vgName, k8sClient)
	if err != nil {
		log.Error("failed to initialize driver", "error", err)
		os.Exit(1)
	}

	driver.Run(ctx)
}

func createK8sClient(log *slog.Logger) (client.Client, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}

	log.Info("k8s client created for drbd support")
	return c, nil
}
