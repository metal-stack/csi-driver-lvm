package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
	"google.golang.org/grpc"
)

var (
	vendorVersion = "dev"
)

type Driver struct {
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
}

func NewDriver(log *slog.Logger, driverName, nodeId, endpoint string, hostWritePath string, ephemeral bool, maxVolumesPerNode int64, version string, devicesPattern string, vgName string) (*Driver, error) {
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
	}, nil
}

func (d *Driver) Run(ctx context.Context) {
	_ = os.Remove(d.endpoint)

	listener, err := net.Listen("unix", d.endpoint)
	if err != nil {
		panic(err)
	}

	server := grpc.NewServer(grpc.ChainUnaryInterceptor(d.requestInterceptorFn))

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
		debug = d.log.Enabled(ctx, slog.LevelDebug)
		start = time.Now()
	)

	if debug {
		log = log.With("request", req)
	}

	log.Info("handling unary call")

	response, err := handler(ctx, req)

	if debug && response != nil {
		log = log.With("response", response)
	}

	if err != nil {
		log.Error("error during unary call", "error", err)
	} else if debug {
		log.Debug("handled call successfully", "duration", time.Since(start).String())
	}

	return resp, err
}
