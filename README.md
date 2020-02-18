
# DO NOT USE FOR PRODUCTION #

### Currently it can create, delete, mount, unmount and resize block and filesystem volumes via lvm.

### Todo:
* implement CreateSnapshot(), ListSnapshots(), DeleteSnapshot()

### Development

TL;DR: ./start.sh (./start-with-psp.sh for minikube with podSecurityPolicy enabled)


#### Start minikube and create dummy volumes:
```
minikube start --memory 4g
minikube ssh 'for i in 0 1; do fallocate -l 1G loop${i} ; sudo losetup -f loop${i}; sudo losetup -a ; done'
```

On minikube we have to copy a "real" losetup:

In minikube losetup is a symlink to busybox.

The busybox implentation of losetup lacks some flags on which the kubernetes currently depends on.
(see https://github.com/kubernetes/kubernetes/issues/83265 )

```
 minikube ssh 'sudo rm /sbin/losetup'
 scp -o 'StrictHostKeyChecking=no' -i $(minikube ssh-key) /usr/sbin/losetup  docker@$(minikube ip):/tmp/losetup
 minikube ssh 'sudo mv /tmp/losetup /sbin/losetup'
```

#### Build:
```
make
docker build
docker push
```

Replace mwennrich/lvmplugin:latest image in helm/values.yaml


#### Deploy:
```
helm install mytest helm
```

#### Test:
```
kubectl apply -f examples/csi-pvc-raw.yaml
kubectl apply -f examples/csi-pod-raw.yaml


kubectl apply -f examples/csi-pvc.yaml
kubectl apply -f examples/csi-app.yaml

kubectl delete -f examples/csi-pod-raw.yaml
kubectl delete -f examples/csi-pvc-raw.yaml

kubectl delete -f  examples/csi-app.yaml
kubectl delete -f examples/csi-pvc.yaml
```
