# csi-driver-lvm #

csi-driver-lvm utilizes local storage of Kubernetes nodes to provide persistent storage for pods.

It automatically creates hostPath based persistent volumes on the nodes.

Underneath it creates a LVM logical volume on the local disks. A comma-separated list of grok pattern, which disks to use must be specified.

This CSI driver is derived from [csi-driver-host-path](https://github.com/kubernetes-csi/csi-driver-host-path) and [csi-lvm](https://github.com/metal-stack/csi-lvm)

> [!WARNING]
> Note that there is always an inevitable risk of data loss when working with local volumes. For this reason, be sure to back up your data or implement proper data replication methods when using this CSI driver.

## Currently it can create, delete, mount, unmount, resize, snapshot and restore block and filesystem volumes via lvm ##

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

2. Enable the encrypted StorageClasses in your Helm values (they are disabled by default):

```yaml
storageClasses:
  linearEncrypted:
    enabled: true
  mirrorEncrypted:
    enabled: true
  stripedEncrypted:
    enabled: true
```

3. Create PVCs using one of the encrypted StorageClasses. The encryption is handled transparently by the driver.

### How it works ###

- **NodeStageVolume**: LUKS-formats the LV (first use only), then opens it via `cryptsetup luksOpen`, creating a `/dev/mapper/csi-lvm-<volumeID>` device
- **NodePublishVolume**: Mounts the mapper device (instead of the raw LV) to the target path
- **NodeUnpublishVolume**: Unmounts as usual
- **NodeUnstageVolume**: Closes the LUKS device via `cryptsetup luksClose`
- **Volume expansion**: The LV is extended first, then the LUKS layer is resized, then the filesystem

Both filesystem and raw block access types are supported with encryption.

### Encrypted Ephemeral Volumes ###

Encryption is also supported for CSI ephemeral (inline) volumes. Since ephemeral volumes bypass `NodeStageVolume`, the LUKS formatting and opening is handled directly during `NodePublishVolume`, and the LUKS device is closed during `NodeUnpublishVolume`.

To use an encrypted ephemeral volume, specify `encryption: "true"` in `volumeAttributes` and reference the encryption secret via `nodePublishSecretRef`:

```yaml
volumes:
  - name: encrypted-ephemeral
    csi:
      driver: lvm.csi.metal-stack.io
      volumeAttributes:
        size: "100Mi"
        type: "linear"
        encryption: "true"
      nodePublishSecretRef:
        name: csi-lvm-encryption-secret
```

## Snapshots ##

csi-driver-lvm supports `VolumeSnapshot`s as thick copy-on-write LVM snapshots, and restoring them into new volumes. Restoring never merges the snapshot back into its source: it creates a new LV and copies the snapshot's data into it, leaving both the snapshot and the source volume untouched.

### Prerequisites ###

Snapshots need the snapshot CRDs and the `snapshot-controller` from [external-snapshotter](https://github.com/kubernetes-csi/external-snapshotter), installed once per cluster:

```bash
VER=v8.5.0
kubectl kustomize "https://github.com/kubernetes-csi/external-snapshotter/client/config/crd?ref=${VER}" | kubectl create -f -
kubectl -n kube-system kustomize "https://github.com/kubernetes-csi/external-snapshotter/deploy/kubernetes/snapshot-controller?ref=${VER}" | kubectl create -f -
```

Each volume lives in the volume group of a single node, so only the driver on that node can snapshot it. The `csi-snapshotter` sidecar therefore runs in every plugin pod with `--node-deployment` and only handles snapshots labeled for its own node. The `snapshot-controller` only sets that label when it runs with distributed snapshotting enabled, which needs a flag and read access to nodes:

```bash
kubectl -n kube-system patch deploy snapshot-controller --type=json \
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--enable-distributed-snapshotting"}]'
kubectl patch clusterrole snapshot-controller-runner --type=json \
  -p '[{"op":"add","path":"/rules/-","value":{"apiGroups":[""],"resources":["nodes"],"verbs":["get","list","watch"]}}]'
```

Without distributed snapshotting, `VolumeSnapshot`s stay `readyToUse: false` forever.

### Setup ###

Enable snapshots in your Helm values. This adds the `csi-snapshotter` sidecar, its RBAC and a `VolumeSnapshotClass` named `csi-driver-lvm`:

```yaml
snapshots:
  enabled: true
```

Then snapshot a PVC and restore it, see [examples/csi-volumesnapshot.yaml](examples/csi-volumesnapshot.yaml) and [examples/csi-pvc-restore.yaml](examples/csi-pvc-restore.yaml):

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: csi-pvc-restore
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 20Mi
  storageClassName: csi-driver-lvm-linear
  dataSource:
    apiGroup: snapshot.storage.k8s.io
    kind: VolumeSnapshot
    name: csi-pvc-snapshot
```

### Snapshot size ###

A thick snapshot reserves copy-on-write space in the volume group. It becomes permanently invalid once more data changes on the source than fits into that space. The size is set by `VolumeSnapshotClass` parameters:

| Parameter             | Description                                                                                          |
|-----------------------|------------------------------------------------------------------------------------------------------|
| `snapshotSizePercent` | Copy-on-write space relative to the source volume, `1` to `100`. Defaults to `100`, which can never overflow. |
| `snapshotSize`        | Absolute copy-on-write space, e.g. `2Gi`. Takes precedence over `snapshotSizePercent`.              |

An invalid snapshot is reported by `ListSnapshots` with `readyToUse: false` and cannot be restored. The `VolumeSnapshot` object itself keeps `readyToUse: true`, because the snapshotter does not re-check snapshots that were ready once.

### Caveats ###

- **Node locality**: a snapshot can only be restored on the node that holds it. Pin pods using a restored PVC to that node, e.g. with a `nodeSelector`. If the scheduler picks another node, provisioning fails with `ResourceExhausted` and the claim is rescheduled.
- **Consistency**: snapshots are crash-consistent only. Quiesce the application (e.g. with pre-snapshot hooks) before creating a `VolumeSnapshot` if you need application consistency.
- **Source volumes with snapshots**: deleting a volume that still has snapshots is refused, because LVM would remove the snapshots with it. Expanding such a volume waits until its snapshots are deleted, because LVM cannot resize an active snapshot origin.
- **Write performance**: every write to a volume with thick snapshots also copies the old block, so keep snapshots only as long as needed.
- **Restore duration**: a restore copies the whole source volume inside `CreateVolume`. The provisioner timeout is raised to `120s` (`provisioner.timeout`), and a copy that takes longer continues in the background across provisioner retries. A copy interrupted by a driver restart starts over.
- **Filesystems**: a restored volume keeps the filesystem UUID of its source, so xfs volumes are always mounted with `nouuid`. A restore larger than its snapshot has its filesystem grown on mount.
- **Encryption**: a snapshot of an encrypted volume contains LUKS ciphertext. Restore it through an encrypted StorageClass that uses the same passphrase secret.
- **Volume mode**: a snapshot of a block volume can only be restored as a block volume, and a snapshot of a filesystem volume only as a filesystem volume. Snapshots of volumes created before this check was added are not checked.
- **Cloning**: PVC-to-PVC cloning is not supported.
- **ListSnapshots**: each plugin pod only lists the snapshots of its own node.

## Migration ##

If you want to migrate your existing PVC to / from csi-driver-lvm, you can use [korb](https://github.com/BeryJu/korb).


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
