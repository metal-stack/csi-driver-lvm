# CSI Driver LVM Helm Chart

## TL;DR

```
$ helm install my-csi-driver-lvm --namespace default csi-driver-lvm --set lvm.devicePattern='/dev/nvme[0-9]n[0-9]'
```
