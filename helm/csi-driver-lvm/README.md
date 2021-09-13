# DEPRECATED

***Please do not use the helm chart from this repository anymore. Instead use the helm chart from <https://helm.metal-stack.io/>***

(To get the previous old and now deprecated `csi-lvm-sc-linear, ...` storageclass names, set helm-chart value `compat03x=true`.)

```bash
helm install csi-driver-lvm csi-driver-lvm -n csi-driver-lvm --set lvm.devicePattern='/dev/nvme[0-1]n[0-9]' --repo https://helm.metal-stack.io/
```
