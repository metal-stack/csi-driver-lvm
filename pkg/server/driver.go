package server

import (
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

var (
	vendorVersion = "dev"
)

type Driver struct {
	sync.Mutex

	csi.UnimplementedNodeServer
	csi.UnimplementedIdentityServer
	csi.UnimplementedControllerServer

	name              string
	nodeId            string
	version           string
	endpoint          string
	hostWritePath     string
	ephemeral         bool
	maxVolumesPerNode int64
	devicesPattern    string
	vgName            string

	server *grpc.Server
}

func NewDriver(driverName, nodeId, endpoint string, hostWritePath string, ephemeral bool, maxVolumesPerNode int64, version string, devicesPattern string, vgName string) (*Driver, error) {
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

	klog.Infof("Driver: %v ", driverName)
	klog.Infof("Version: %s", vendorVersion)

	return &Driver{
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

func (d *Driver) Run() {
	_ = os.Remove(d.endpoint)

	listener, err := net.Listen("unix", d.endpoint)
	if err != nil {
		panic(err)
	}

	d.server = grpc.NewServer()
	csi.RegisterIdentityServer(d.server, d)
	csi.RegisterControllerServer(d.server, d)
	csi.RegisterNodeServer(d.server, d)

	_ = d.server.Serve(listener)
}
