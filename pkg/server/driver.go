package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"github.com/metal-stack/v"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	vendorVersion = "dev"
)

type Driver struct {
	sync.Mutex

	csi.UnimplementedNodeServer
	csi.UnimplementedIdentityServer
	csi.UnimplementedControllerServer

	log               *slog.Logger
	name              string
	nodeId            string
	version           string
	endpoint          string
	hostWritePath     string
	ephemeral         bool
	maxVolumesPerNode int64
	devicesPattern    string
	vgName            string
	k8sClient         client.Client
}

func NewDriver(log *slog.Logger, driverName, nodeId, endpoint string, hostWritePath string, ephemeral bool, maxVolumesPerNode int64, version string, devicesPattern string, vgName string, k8sClient client.Client) (*Driver, error) {
	if driverName == "" {
		return nil, fmt.Errorf("no driver name provided")
	}
	if nodeId == "" {
		return nil, fmt.Errorf("no node id provided")
	}
	if endpoint == "" {
		return nil, fmt.Errorf("no driver endpoint provided")
	}
	if version != "" {
		vendorVersion = version
	}

	log.Info("ensuring vg setup")

	vgexists := lvm.VgExists(log, vgName)
	if !vgexists {
		log.Info("vg not found", "vgName", vgName)
		lvm.VgActivate(log)
		// now check again for existing vg again
		vgexists := lvm.VgExists(log, vgName)
		if !vgexists {
			log.Info("vg still not existing - creating...", "vgName", vgName)
			_, err := lvm.CreateVG(log, vgName, devicesPattern)
			if err != nil {
				return nil, fmt.Errorf("unable to create initial volume group: %w", err)
			}
		}
	}

	log.Info("initializing driver", "name", driverName, "endpoint", endpoint, "hostWritePath", hostWritePath, "ephemeral", ephemeral, "maxVolumesPerNode", maxVolumesPerNode, "devicesPattern", devicesPattern, "vgName", vgName)

	return &Driver{
		log:               log,
		name:              driverName,
		version:           vendorVersion,
		nodeId:            nodeId,
		endpoint:          endpoint,
		hostWritePath:     hostWritePath,
		ephemeral:         ephemeral,
		maxVolumesPerNode: maxVolumesPerNode,
		devicesPattern:    devicesPattern,
		vgName:            vgName,
		k8sClient:         k8sClient,
	}, nil
}

func (d *Driver) Run(ctx context.Context) {
	_ = os.Remove(d.endpoint)

	listener, err := net.Listen("unix", d.endpoint)
	if err != nil {
		panic(err)
	}

	d.log.Info("starting grpc server", "application-version", v.V.String())

	server := grpc.NewServer(grpc.UnaryInterceptor(d.requestInterceptorFn))

	csi.RegisterIdentityServer(server, d)
	csi.RegisterControllerServer(server, d)
	csi.RegisterNodeServer(server, d)

	go func() {
		if err := server.Serve(listener); err != nil {
			d.log.Error("error serving grpc, server stopped", "error", err)
		}
	}()

	<-ctx.Done()

	d.log.Info("received signal, shutting down the server...")

	server.GracefulStop()

	d.log.Info("server stopped gracefully")
}

func (d *Driver) requestInterceptorFn(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	var (
		log   = d.log.With("method", info.FullMethod)
		start = time.Now()
	)

	response, err := handler(ctx, req)

	log.With("duration", time.Since(start).String())

	if err != nil {
		log.Error("error during unary call", "error", err)
	} else {
		log.Debug("handled call successfully")
	}

	return response, err
}
