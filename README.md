# csi-driver-lvm #

CSI DRIVER LVM utilizes local storage of Kubernetes nodes to provide persistent storage for pods.

It automatically creates hostPath based persistent volumes on the nodes.

Underneath it creates a LVM logical volume on the local disks. A comma-separated list of grok pattern, which disks to use must be specified.

This CSI driver is derived from [csi-driver-host-path](https://github.com/kubernetes-csi/csi-driver-host-path) and [csi-lvm](https://github.com/metal-stack/csi-lvm)

## Currently it can create, delete, mount, unmount and resize block and filesystem volumes via lvm ##

For the special case of block volumes, the filesystem-expansion has to be perfomend by the app using the block device

## Installation via official helm chart ##

You have to set the devicePattern for your hardware to specify which disks should be used to create the volume group.

```bash
helm install csi-driver-lvm csi-driver-lvm -n csi-driver-lvm --set lvm.devicePattern='/dev/nvme[0-1]n[0-9]' --repo https://helm.metal-stack.io/
```

Now you can use one of following storageClasses:

* `csi-driver-lvm-mirror`
* `csi-driver-lvm-linear`
* `csi-driver-lvm-striped`

(to get the previous old and now deprecated `csi-lvm-sc-linear, ...` storageclasses, set helm-chart value `compat03x=true` )

## Installation from local helm directory ##

***Please do not use the helm chart from this repository anymore. Instead use the helm chart from <https://helm.metal-stack.io/>***

Migration instructions:

```bash
kubectl delete csidrivers.storage.k8s.io lvm.csi.metal-stack.io
helm upgrade csi-driver-lvm csi-driver-lvm -n csi-driver-lvm --set lvm.devicePattern='/dev/nvme[0-1]n[0-9]' --set comapt03x=true --repo https://helm.metal-stack.io/
```

## Todo ##

* implement CreateSnapshot(), ListSnapshots(), DeleteSnapshot()

## Development ###

TL;DR:

```bash
./start-minikube-on-linux.sh
helm install mytest helm/csi-driver-lvm --set lvm.devicePattern='/dev/loop[0-1]'
```
