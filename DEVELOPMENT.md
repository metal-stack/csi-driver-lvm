# Development #

TL;DR:

```text
$ ./start-minikube-on-linux.sh
helm install mytest charts/csi-driver-lvm --set lvm.devicePattern='/dev/loop[0-1]'
```

## Start minikube and create dummy volumes ##

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

## Build ##

```bash
make
docker build
docker push
```

Replace metalstack/lvmplugin:latest image in charts/csi-driver-lvm/values.yaml

### Deploy ###

```bash
helm install mytest charts/csi-driver-lvm --set lvm.devicePattern='/dev/loop[0-1]'
```

### Test ###

```bash
kubectl apply -f examples/csi-pvc-raw.yaml
kubectl apply -f examples/csi-pod-raw.yaml

kubectl apply -f examples/csi-pvc.yaml
kubectl apply -f examples/csi-app.yaml

kubectl delete -f examples/csi-pod-raw.yaml
kubectl delete -f examples/csi-pvc-raw.yaml

kubectl delete -f examples/csi-app.yaml
kubectl delete -f examples/csi-pvc.yaml
```
