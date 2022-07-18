# csi-driver-lvm #

CSI DRIVER LVM utilizes local storage of Kubernetes nodes to provide persistent storage for pods.

It automatically creates hostPath based persistent volumes on the nodes.

Underneath it creates a LVM logical volume on the local disks. A comma-separated list of grok pattern, which disks to use must be specified.

This CSI driver is derived from [csi-driver-host-path](https://github.com/kubernetes-csi/csi-driver-host-path) and [csi-lvm](https://github.com/metal-stack/csi-lvm)

## Currently it can create, delete, mount, unmount and resize block and filesystem volumes via lvm ##

For the special case of block volumes, the filesystem-expansion has to be performed by the app using the block device

## Installation ##

You have to set the devicePattern for your hardware to specify which disks should be used to create the volume group.

```bash
helm install mytest helm/csi-driver-lvm --set lvm.devicePattern='/dev/nvme[0-9]n[0-9]'
```

Now you can use one of following storageClasses:

* `csi-driver-lvm-linear`
* `csi-driver-lvm-mirror`
* `csi-driver-lvm-striped`

## Migration ##

If you want to migrate your existing PVC to / from csi-driver-lvm, you can use [korb](https://github.com/BeryJu/korb).

### Todo ###

* implement CreateSnapshot(), ListSnapshots(), DeleteSnapshot()

## Development ###

TL;DR:

```bash
./start-minikube-on-linux.sh
helm install mytest helm/csi-driver-lvm --set lvm.devicePattern='/dev/loop[0-1]'
```

### Start minikube and create dummy volumes ###

```bash
minikube start --memory 4g
minikube ssh 'for i in 0 1; do fallocate -l 1G loop${i} ; sudo losetup -f loop${i}; sudo losetup -a ; done'
```

On minikube we have to copy a "real" losetup:

In minikube losetup is a symlink to busybox.

The busybox implentation of losetup lacks some flags on which the kubernetes currently depends on.
(see <https://github.com/kubernetes/kubernetes/issues/83265> )

```bash
 minikube ssh 'sudo rm /sbin/losetup'
 scp -o 'StrictHostKeyChecking=no' -i $(minikube ssh-key) /usr/sbin/losetup  docker@$(minikube ip):/tmp/losetup
 minikube ssh 'sudo mv /tmp/losetup /sbin/losetup'
```

### Build ###

```bash
make
docker build
docker push
```

Replace metalstack/lvmplugin:latest image in helm/csi-driver-lvm/values.yaml

### Deploy ###

```bash
helm install mytest helm/csi-driver-lvm
```

### Test ###

```bash
kubectl apply -f examples/csi-pvc-raw.yaml
kubectl apply -f examples/csi-pod-raw.yaml


kubectl apply -f examples/csi-pvc.yaml
kubectl apply -f examples/csi-app.yaml

kubectl delete -f examples/csi-pod-raw.yaml
kubectl delete -f examples/csi-pvc-raw.yaml

kubectl delete -f  examples/csi-app.yaml
kubectl delete -f examples/csi-pvc.yaml
```
