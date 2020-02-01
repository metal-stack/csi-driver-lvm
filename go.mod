module github.com/mwennrich/csi-driver-lvm

go 1.13

require (
	github.com/container-storage-interface/spec v1.1.0
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/lint v0.0.0-20180702182130-06c8688daad7 // indirect
	github.com/golang/protobuf v1.3.2
	github.com/google/lvmd v0.0.0-20190916151813-e6e28ff087f6
	github.com/google/uuid v1.0.0 // indirect
	github.com/kubernetes-csi/csi-lib-utils v0.3.0
	github.com/pborman/uuid v0.0.0-20180906182336-adf5a7427709
	github.com/spf13/afero v1.2.2 // indirect
	github.com/stretchr/testify v1.4.0 // indirect
	github.com/urfave/cli v1.22.2
	golang.org/x/net v0.0.0-20190912160710-24e19bdeb0f2
	google.golang.org/grpc v1.23.1
	k8s.io/apimachinery v0.0.0-20181110190943-2a7c93004028 // indirect
	k8s.io/klog v1.0.0
	k8s.io/kubernetes v1.12.2
	k8s.io/utils v0.0.0-20181102055113-1bd4f387aa67
)

replace github.com/mwennrich/csi-driver-lvm/pkg/lvm => ./pkg/lvm
