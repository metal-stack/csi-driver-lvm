# csi-driver-lvm #

CSI DRIVER LVM utilizes local storage of Kubernetes nodes to provide persistent storage for pods.

It automatically creates local persistent volumes on the nodes.

Underneath it creates a LVM logical volume on the local disks. A comma-separated list of grok pattern, which disks to use must be specified.

This CSI driver is derived from [csi-driver-host-path](https://github.com/kubernetes-csi/csi-driver-host-path) and [csi-lvm](https://github.com/metal-stack/csi-lvm )


**Currently it can create, delete, mount, unmount and resize block and filesystem volumes via lvm**

For the special case of block volumes, the filesystem-expansion has to be perfomend by the app using the block device

For usage of S3-backed volumeSnapshots, see [SNAPSHOTS.md](SNAPSHOTS.md)

## Installation ##

You have to set the devicePattern for your hardware to specify which disks should be used to create the volume group.

```bash
helm install csi-driver-lvm csi-driver-lvm -n csi-driver-lvm --repo https://metal-stack.github.io/csi-driver-lvm --set lvm.devicePattern='/dev/nvme[0-9]n[0-9]'
```

Now you can use one of following storageClasses:

* `csi-driver-lvm-mirror`
* `csi-driver-lvm-linear`
* `csi-driver-lvm-striped`

## Upgrading an existing release to a new version ##

### From 0.3.x to 0.4.x ###

***The default storageclass names installed by the helm chart have changed from csi-lvm-sc-linear to csi-driver-lvm-linear***

To additionally install the old storageclass names, use `--set compat03x=true`

## Development ##

see [DEVELOPMENT.md](DEVELOPMENT.md)
