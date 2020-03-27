```
helm install csi-lvm-driver helm
kubectl apply -f examples/rook/common.yaml
kubectl apply -f examples/rook/operator.yaml
kubectl apply -f examples/rook/cluster-on-lvm.yaml
kubectl apply -f examples/rook/storageclass.yaml
kubectl apply -f examples/rook/psp.yaml
kubectl apply -f examples/rook/mysql.yaml
kubectl apply -f examples/rook/wordpress.yaml
```
