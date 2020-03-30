### Example files for installation of rook on csi-lvm backed PVCs

* install csi-driver-lvm (see ../../README.md)
* install rook operator
* install rook cluster
* install rook storage classes
* install basic psp for the mysql/wordpress example (if needed)
* install a single mysql instance on a rook-ceph-block ReadWriteOnce PVC
* install cephfs filesystem
* install a wordpress deployment with 3 replicas on a cephfs shared ReadWriteMany filesytem


```
kubectl apply -f examples/rook/common.yaml
kubectl apply -f examples/rook/operator.yaml
kubectl apply -f examples/rook/cluster-on-lvm.yaml
kubectl apply -f examples/rook/storageclass-rbd.yaml
kubectl apply -f examples/rook/storageclass-cephfs.yaml
kubectl apply -f examples/rook/psp.yaml
kubectl apply -f examples/rook/mysql.yaml
kubectl apply -f examples/rook/filesystem.yaml
kubectl apply -f examples/rook/wordpress.yaml
```
