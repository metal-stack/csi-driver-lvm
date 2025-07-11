# csi-driver-lvm #

CSI DRIVER LVM utilizes local storage of Kubernetes nodes to provide persistent storage for pods.

It automatically creates hostPath based persistent volumes on the nodes.

Underneath it creates a LVM logical volume on the local disks. A comma-separated list of grok pattern, which disks to use must be specified.

This CSI driver is derived from [csi-driver-host-path](https://github.com/kubernetes-csi/csi-driver-host-path) and [csi-lvm](https://github.com/metal-stack/csi-lvm)

## Currently it can create, delete, mount, unmount and resize block and filesystem volumes via lvm ##

For the special case of block volumes, the filesystem-expansion has to be performed by the app using the block device

## Pod eviction

In case of pod eviction the pod the pvc doesn't get deleted. Therefore the pod can't start on another node due to node-affinity. With the `csi-driver-lvm-controller` it's able to capture these events and delete the pvc.

Following is needed:

- statefulset with volumeClaimTemplate and reference to `csi-driver-lvm` storageclass
- pvc needs annotation: `metal-stack.io/csi-driver-lvm.is-eviction-allowed: true`

## Installation ##

**Helm charts for installation are located in a separate repository called [helm-charts](https://github.com/metal-stack/helm-charts). If you would like to contribute to the helm chart, please raise an issue or pull request there.**

You have to set the devicePattern for your hardware to specify which disks should be used to create the volume group.

```bash
helm install --repo https://helm.metal-stack.io mytest csi-driver-lvm --set lvm.devicePattern='/dev/nvme[0-9]n[0-9]'
```

Now you can use one of following storageClasses:

* `csi-driver-lvm-linear`
* `csi-driver-lvm-mirror`
* `csi-driver-lvm-striped`

To get the previous old and now deprecated `csi-lvm-sc-linear`, ... storageclasses, set helm-chart value `compat03x=true`.

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
