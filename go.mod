module github.com/mwennrich/csi-driver-lvm

go 1.13

require (
	github.com/container-storage-interface/spec v1.1.0
	github.com/docker/go-units v0.4.0
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/lint v0.0.0-20180702182130-06c8688daad7 // indirect
	github.com/golang/protobuf v1.3.2
	github.com/google/gofuzz v1.1.0 // indirect
	github.com/google/lvmd v0.0.0-20190916151813-e6e28ff087f6
	github.com/json-iterator/go v1.1.9 // indirect
	github.com/kubernetes-csi/csi-lib-utils v0.3.0
	github.com/pborman/uuid v0.0.0-20180906182336-adf5a7427709
	github.com/urfave/cli v1.22.2
	github.com/urfave/cli/v2 v2.1.1
	golang.org/x/net v0.0.0-20191004110552-13f9640d40b9
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0 // indirect
	google.golang.org/grpc v1.23.1
	k8s.io/api v0.17.3
	k8s.io/apimachinery v0.17.3
	k8s.io/client-go v0.17.3
	k8s.io/klog v1.0.0
	k8s.io/kube-aggregator v0.17.3 // indirect
	k8s.io/kubernetes v1.12.2
	k8s.io/utils v0.0.0-20200124190032-861946025e34
	sigs.k8s.io/yaml v1.2.0 // indirect
)

replace github.com/mwennrich/csi-driver-lvm/pkg/lvm => ./pkg/lvm
