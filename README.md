# csi-driver-lvm #

csi-driver-lvm utilizes local storage of Kubernetes nodes to provide persistent storage for pods.

It automatically creates hostPath based persistent volumes on the nodes.

Underneath it creates a LVM logical volume on the local disks. A comma-separated list of grok pattern, which disks to use must be specified.

This CSI driver is derived from [csi-driver-host-path](https://github.com/kubernetes-csi/csi-driver-host-path) and [csi-lvm](https://github.com/metal-stack/csi-lvm)

> [!WARNING]
> Note that there is always an inevitable risk of data loss when working with local volumes. For this reason, be sure to back up your data or implement proper data replication methods when using this CSI driver.

## Currently it can create, delete, mount, unmount and resize block and filesystem volumes via lvm ##

For the special case of block volumes, the filesystem-expansion has to be performed by the app using the block device

## Automatic PVC Deletion on Pod Eviction

The persistent volumes created by this CSI driver are strictly node-affine to the node on which the pod was scheduled. This is intentional and prevents pods from starting without the LV data, which resides only on the specific node in the Kubernetes cluster.

Consequently, if a pod is evicted (potentially due to cluster autoscaling or updates to the worker node), the pod may become stuck. In certain scenarios, it's acceptable for the pod to start on another node, despite the potential for data loss. The csi-driver-lvm-controller can capture these events and automatically delete the PVC without requiring manual intervention by an operator.

To use this functionality, the following is needed:

- This only works on `StatefulSet`s with volumeClaimTemplates and volume references to the `csi-driver-lvm` storage class
- In addition to that, the `Pod` or `PersistentVolumeClaim` managed by the `StatefulSet` needs the annotation: `metal-stack.io/csi-driver-lvm.is-eviction-allowed: true`

## Installation ##

**For convenience, helm charts for installation are synced to a separate repository called [helm-charts](https://github.com/metal-stack/helm-charts). The source for this chart is located in the `charts` folder.**

You have to set the `devicePattern` for your hardware to specify which disks should be used to create the volume group.

```bash
helm install csi-driver-lvm ./charts/csi-driver-lvm --set lvm.devicePattern='/dev/nvme[0-9]n[0-9]'
# or alternatively after the a release:
# helm install --repo https://helm.metal-stack.io csi-driver-lvm csi-driver-lvm --set lvm.devicePattern='/dev/nvme[0-9]n[0-9]'
```

Now you can use one of following storageClasses:

* `csi-driver-lvm-linear`
* `csi-driver-lvm-mirror`
* `csi-driver-lvm-striped`
* `csi-driver-lvm-linear-encrypted`
* `csi-driver-lvm-mirror-encrypted`
* `csi-driver-lvm-striped-encrypted`

To get the previous old and now deprecated `csi-lvm-sc-linear`, ... storageclasses, set helm-chart value `compat03x=true`.

## Encryption ##

csi-driver-lvm supports LUKS2 encryption for volumes at rest. When encryption is enabled, the LVM logical volume is formatted with LUKS2 and a dm-crypt mapper device is used transparently for all I/O.

### Setup ###

1. Create a Kubernetes Secret containing the LUKS passphrase:

```bash
kubectl create secret generic csi-lvm-encryption-secret \
  --from-literal=passphrase='my-secret-passphrase'
```

2. Create PVCs using one of the encrypted StorageClasses. The encryption is handled transparently by the driver.

### How it works ###

- **NodeStageVolume**: LUKS-formats the LV (first use only), then opens it via `cryptsetup luksOpen`, creating a `/dev/mapper/csi-lvm-<volumeID>` device
- **NodePublishVolume**: Mounts the mapper device (instead of the raw LV) to the target path
- **NodeUnpublishVolume**: Unmounts as usual
- **NodeUnstageVolume**: Closes the LUKS device via `cryptsetup luksClose`
- **Volume expansion**: The LV is extended first, then the LUKS layer is resized, then the filesystem

Both filesystem and raw block access types are supported with encryption.

## Migration ##

If you want to migrate your existing PVC to / from csi-driver-lvm, you can use [korb](https://github.com/BeryJu/korb).

### Todo ###

* implement CreateSnapshot(), ListSnapshots(), DeleteSnapshot()


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

### Development ###

In order to run the integration tests locally, you need to create to loop devices on your host machine. Make sure the loop device mount paths are not used on your system (default path is `/dev/loop10{0,1}`).

You can create these loop devices like this:

```bash
for i in 100 101; do fallocate -l 1G loop${i}.img ; sudo losetup /dev/loop${i} loop${i}.img; done
sudo losetup -a
# https://github.com/util-linux/util-linux/issues/3197
# use this for recreation or cleanup
# for i in 100 101; do sudo losetup -d /dev/loop${i}; rm -f loop${i}.img; done
```

You can then run the tests against a kind cluster, running:

```bash
make test
```

To recreate or cleanup the kind cluster:

```bash
make test-cleanup
```
