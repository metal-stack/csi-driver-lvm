module github.com/metal-stack/csi-driver-lvm

go 1.14

require (
	github.com/container-storage-interface/spec v1.2.0
	github.com/docker/go-units v0.4.0
	github.com/golang/protobuf v1.3.3 // indirect
	github.com/google/gofuzz v1.1.0 // indirect
	github.com/google/lvmd v0.0.0-20190916151813-e6e28ff087f6
	github.com/googleapis/gnostic v0.4.0 // indirect
	github.com/json-iterator/go v1.1.9 // indirect
	github.com/kubernetes-csi/csi-lib-utils v0.7.0
	github.com/urfave/cli/v2 v2.1.1
	golang.org/x/net v0.0.0-20191209160850-c0dbc17a3553
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0 // indirect
	google.golang.org/grpc v1.27.1
	k8s.io/api v0.17.3
	k8s.io/apimachinery v0.17.3
	k8s.io/client-go v0.17.3
	k8s.io/klog v1.0.0
	k8s.io/utils v0.0.0-20200124190032-861946025e34 // indirect
	sigs.k8s.io/yaml v1.2.0 // indirect
)
