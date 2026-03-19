# csi-driver-lvm #

csi-driver-lvm utilizes local storage of Kubernetes nodes to provide persistent storage for pods.

It automatically creates hostPath based persistent volumes on the nodes.

Underneath it creates a LVM logical volume on the local disks. A comma-separated list of grok pattern, which disks to use must be specified.

This CSI driver is derived from [csi-driver-host-path](https://github.com/kubernetes-csi/csi-driver-host-path) and [csi-lvm](https://github.com/metal-stack/csi-lvm)

> [!WARNING]
> Note that there is always an inevitable risk of data loss when working with non-replicated local volumes. For this reason, be sure to back up your data or enable DRBD replication when using this CSI driver.

## Currently it can create, delete, mount, unmount and resize block and filesystem volumes via lvm ##

For the special case of block volumes, the filesystem-expansion has to be performed by the app using the block device

## DRBD Replication

csi-driver-lvm supports optional synchronous replication of volumes to a second node in the cluster using [DRBD](https://linbit.com/drbd/). When enabled, each replicated volume maintains a real-time copy on a standby node. If the primary node fails, the pod and its PVC are automatically failed over to the standby node **without data loss**.

### How it works

```
  Node A (Primary)                  Node B (Secondary/Standby)
  ┌─────────────────┐              ┌─────────────────┐
  │ LV: vol-abc     │──── DRBD ───▶│ LV: vol-abc     │
  │ /dev/vg/vol-abc │   (sync)     │ /dev/vg/vol-abc │
  │       │         │              │                  │
  │  /dev/drbdX     │              │  /dev/drbdX      │
  │       │         │              │  (Secondary,     │
  │  mounted by pod │              │   not mounted)   │
  └─────────────────┘              └─────────────────┘
```

1. When a PVC is created with the `csi-driver-lvm-replicated` StorageClass, an LV is created on the primary node and a `DRBDVolume` custom resource is created.
2. The DRBD replication controller selects a secondary node using a **least-usage heuristic** (fewest existing replicas, most available capacity).
3. The node agents on both nodes create DRBD resource configs, initialize metadata, and establish replication.
4. The pod mounts the DRBD device (`/dev/drbdN`) instead of the raw LV. All writes are synchronously replicated to the secondary (DRBD protocol C).
5. On node failure, the eviction controller **promotes the secondary** and updates the PV node affinity instead of deleting the PVC. The pod reschedules to the standby node with its data intact.

### Prerequisites

- The DRBD kernel module (`drbd`) must be loaded on all nodes. Many distributions ship it by default. You can verify with `modprobe drbd`.
- At least two nodes with the same LVM volume group available.
- The eviction controller must be enabled for automatic failover.

### Enabling DRBD replication

Enable DRBD support and the replicated StorageClass in the Helm values:

```yaml
drbd:
  enabled: true
  protocol: "C"      # synchronous replication (recommended)

storageClasses:
  replicated:
    enabled: true
    reclaimPolicy: Delete

evictionEnabled: true
```

Install or upgrade the Helm chart:

```bash
helm upgrade --install csi-driver-lvm ./charts/csi-driver-lvm \
  --set lvm.devicePattern='/dev/nvme[0-9]n[0-9]' \
  --set drbd.enabled=true \
  --set storageClasses.replicated.enabled=true \
  --set evictionEnabled=true
```

This creates the `csi-driver-lvm-replicated` StorageClass and deploys the `DRBDVolume` CRD.

### Using replicated volumes

Create a PVC with the replicated StorageClass:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: csi-pvc-replicated
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: csi-driver-lvm-replicated
```

Use it in a Pod:

```yaml
kind: Pod
apiVersion: v1
metadata:
  name: my-csi-app-replicated
spec:
  containers:
    - name: my-frontend
      image: busybox
      volumeMounts:
      - mountPath: "/data"
        name: my-csi-volume
      command: [ "sleep", "1000000" ]
  volumes:
    - name: my-csi-volume
      persistentVolumeClaim:
        claimName: csi-pvc-replicated
```

### Using replicated volumes with StatefulSets (recommended for failover)

For automatic failover on node failure, use a StatefulSet with `volumeClaimTemplates` and the eviction annotation:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: nginx-replicated
spec:
  serviceName: "nginx-replicated"
  replicas: 1
  selector:
    matchLabels:
      app: nginx-replicated
  template:
    metadata:
      labels:
        app: nginx-replicated
      annotations:
        metal-stack.io/csi-driver-lvm.is-eviction-allowed: "true"
    spec:
      containers:
      - name: nginx
        image: nginx:1.14.2
        volumeMounts:
        - mountPath: "/data"
          name: data
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      accessModes: ["ReadWriteOnce"]
      storageClassName: csi-driver-lvm-replicated
      resources:
        requests:
          storage: 1Gi
```

When the primary node goes down or becomes unschedulable, the eviction controller will:

1. Detect the node failure
2. Promote the DRBD secondary to primary
3. Update the PV node affinity to point to the new primary
4. The StatefulSet controller reschedules the pod to the new node with data intact

### Inspecting DRBD volume state

`DRBDVolume` is a cluster-scoped custom resource. You can inspect replication state with:

```bash
kubectl get drbdvolumes
```

```
NAME       PRIMARY   SECONDARY   PHASE         CONNECTION
pvc-abc    node-a    node-b      Established   Connected
pvc-def    node-c    node-a      Established   Connected
```

For detailed status:

```bash
kubectl get drbdvolume pvc-abc -o yaml
```

### Re-establishing redundancy after node replacement

After a failover, the `DRBDVolume` enters the `Degraded` phase. The old failed node is listed as the secondary. The DRBD replication controller periodically checks whether the secondary node is truly gone (Node object deleted, or both unschedulable and NotReady).

**Automatic re-replication:** Once the controller confirms the secondary node is gone **and** the grace period (5 minutes) has elapsed, it automatically selects a new secondary using the same least-usage heuristic, resets the DRBD setup, and the node agents establish replication to the replacement node. The volume transitions back through `SecondaryAssigned` → `PrimaryReady` → `SecondaryReady` → `Established`.

```
Degraded (node-a gone) → SecondaryAssigned (node-c picked) → Established (synced to node-c)
```

If you replaced the physical machine but reused the same node name, the controller sees the node as healthy and waits for it to recover on its own (DRBD reconnects automatically). If the node name changed, the old Node object must be removed from the cluster for re-replication to trigger:

```bash
# Remove the old node object so the controller knows it's gone
kubectl delete node old-node-name

# The controller will automatically select a new secondary
kubectl get drbdvolumes -w
```

**What happens on the new secondary:**
1. The node agent creates a fresh LV
2. Writes the DRBD resource config pointing to the primary
3. Initializes DRBD metadata and brings up the resource
4. DRBD performs a full initial sync from the primary

**What happens on the primary:**
1. The node agent tears down the old DRBD config (pointing to the dead node)
2. Writes a new config pointing to the new secondary
3. Reinitializes and reconnects
4. DRBD syncs all data to the new secondary

The volume remains fully usable during re-replication. The pod continues running on the primary while the sync happens in the background.

### Rolling Kubernetes cluster updates

DRBD replication is designed to work with rolling cluster updates where nodes are rebooted or reimaged one at a time. Two mechanisms ensure stability:

**Grace period:** When a volume enters the `Degraded` phase, the controller records a `degradedSince` timestamp and waits **5 minutes** before triggering re-replication. This prevents unnecessary data movement when a node is simply rebooting during a rolling update. If the node comes back within the grace period and DRBD reconnects, the volume transitions back to `Established` without any re-replication.

**Reimaged node detection:** If a node is reimaged (OS reinstalled) but keeps the same name, the node agent detects that its local LV and DRBD config are missing even though the readiness flag in the `DRBDVolume` CR is still `true`. It automatically resets the readiness flag, which triggers a rebuild of the DRBD resource on that node.

**Recommended rolling update procedure:**

1. Update nodes one at a time, waiting for each node to become `Ready` before proceeding to the next.
2. After each node comes back, verify DRBD volumes are `Established`:
   ```bash
   kubectl get drbdvolumes
   ```
3. Only proceed to the next node once all volumes show `Established` and `Connected`.

This ensures that at any point during the update, at most one side of each DRBD pair is down, and the 5-minute grace period prevents premature re-replication.

### Raw block replicated volumes

DRBD replication also works with raw block volumes:

```yaml
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: pvc-replicated-raw
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: csi-driver-lvm-replicated
  volumeMode: Block
  resources:
    requests:
      storage: 1Gi
```

### Configuration reference

| Helm value | Default | Description |
|------------|---------|-------------|
| `drbd.enabled` | `false` | Enable DRBD replication support |
| `drbd.protocol` | `"C"` | DRBD replication protocol. `C` = synchronous (recommended), `B` = memory-synchronous, `A` = asynchronous |
| `drbd.portRange` | `"7900-7999"` | TCP port range for DRBD replication traffic |
| `drbd.minorRange` | `"100-999"` | DRBD device minor number range |
| `storageClasses.replicated.enabled` | `false` | Create the `csi-driver-lvm-replicated` StorageClass |
| `storageClasses.replicated.reclaimPolicy` | `Delete` | Reclaim policy for replicated volumes |
| `evictionEnabled` | `false` | Enable the eviction controller (required for automatic failover) |

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
* `csi-driver-lvm-replicated` (requires `drbd.enabled=true`, see [DRBD Replication](#drbd-replication))

To get the previous old and now deprecated `csi-lvm-sc-linear`, ... storageclasses, set helm-chart value `compat03x=true`.

## Migration ##

If you want to migrate your existing PVC to / from csi-driver-lvm, you can use [korb](https://github.com/BeryJu/korb).

### Todo ###

* implement CreateSnapshot(), ListSnapshots(), DeleteSnapshot()


### Test ###

```bash
# non-replicated volumes
kubectl apply -f examples/csi-pvc-raw.yaml
kubectl apply -f examples/csi-pod-raw.yaml

kubectl apply -f examples/csi-pvc.yaml
kubectl apply -f examples/csi-app.yaml

kubectl delete -f examples/csi-pod-raw.yaml
kubectl delete -f examples/csi-pvc-raw.yaml

kubectl delete -f  examples/csi-app.yaml
kubectl delete -f examples/csi-pvc.yaml

# replicated volumes (requires drbd.enabled=true)
kubectl apply -f examples/csi-pvc-replicated.yaml
kubectl apply -f examples/csi-app-replicated.yaml

kubectl get drbdvolumes

kubectl delete -f examples/csi-app-replicated.yaml
kubectl delete -f examples/csi-pvc-replicated.yaml

# replicated statefulset with automatic failover
kubectl apply -f examples/csi-statefulset-replicated.yaml

kubectl get drbdvolumes
kubectl get pods -o wide

kubectl delete -f examples/csi-statefulset-replicated.yaml
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
