
# DO NOT USE FOR PRODUCTION #

## This is a barely working copy-paste-hack-job, based on csi-driver-hostpath with working content of csi-lvm.
## Currently it can mount and unmount block and filesystem volumes via lvm. Only tested in minikube.

## Todo:
* a lot
* 


In minikube losetup is a symlink to busybox.

The busybox implentation of losetup lacks some flags on which the kubernetes currently depends on.
(see https://github.com/kubernetes/kubernetes/issues/83265 )

Start minikube and create dummy volumes:
```
minikube start --memory 4g
minikube ssh 'for i in 0 1; do fallocate -l 1G loop${i} ; sudo losetup -f loop${i}; sudo losetup -a ; done'
```

On minikube we have to copy a "real" losetup:

```
 minikube ssh 'sudo rm /sbin/losetup'
 scp -o 'StrictHostKeyChecking=no' -i $(minikube ssh-key) /usr/sbin/losetup  docker@$(minikube ip):/tmp/losetup
 minikube ssh 'sudo mv /tmp/losetup /sbin/losetup'
```

Build:
```
make
docker build
docker push
```

Replace mwennrich/lvmplugin:latest image in deploy/kubernetes-1.17/lvm/csi-lvm-plugin.yaml


Deploy:
```
./deploy/kubernetes-1.17/deploy-lvm.sh
kubectl apply -f examples/csi-storageclass.yaml
```

Test:
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
