#!/usr/bin/env bats -p

@test "deploy csi-lvm-controller" {
    run kubectl create namespace csi-driver-lvm || true
    run helm upgrade --debug --install --repo ${HELM_REPO} --namespace csi-driver-lvm csi-driver-lvm csi-driver-lvm --values values.yaml --wait --timeout=120s
    [ "$status" -eq 0 ]
}

@test "deploy inline pod with ephemeral volume" {
    run kubectl apply -f files/pod.inline.vol.yaml --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "inline pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.inline.vol.yaml --timeout=20s
    [ "$status" -eq 0 ]
}

@test "delete inline linear pod" {
    run kubectl delete -f files/pod.inline.vol.yaml --grace-period=0 --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "create pvc linear" {
    run kubectl apply -f files/pvc.linear.yaml --wait --timeout=20s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.linear.yaml --timeout=20s
    [ "$status" -eq 0 ]
}

@test "deploy linear pod" {
    run kubectl apply -f files/pod.linear.vol.yaml --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "linear pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.linear.vol.yaml --timeout=20s
    [ "$status" -eq 0 ]
}

@test "pvc linear bound" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Bound -f files/pvc.linear.yaml --timeout=20s
    [ "$status" -eq 0 ]
}

@test "resize linear pvc" {
    run kubectl apply -f files/pvc.linear.resize.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    # in some cases a pod restart is required
    run kubectl replace --force -f files/pod.linear.vol.yaml --wait --timeout=50s

    run kubectl wait --for=jsonpath='{.status.capacity.storage}'=200Mi -f files/pvc.linear.resize.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "create block pvc" {
    run kubectl apply -f files/pvc.block.yaml --wait --timeout=20s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.block.yaml --timeout=20s
    [ "$status" -eq 0 ]
}

@test "deploy block pod" {
    run kubectl apply -f files/pod.block.vol.yaml --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "block pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.block.vol.yaml --timeout=20s
    [ "$status" -eq 0 ]
}

@test "pvc block bound" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Bound -f files/pvc.block.yaml --timeout=20s
    [ "$status" -eq 0 ]
}

@test "resize block pvc" {
    run kubectl apply -f files/pvc.block.resize.yaml --wait --timeout=40s
    [ "$status" -eq 0 ]

    # in some cases a pod restart is required
    run kubectl replace --force -f files/pod.block.vol.yaml --wait --timeout=50s

    run kubectl wait --for=jsonpath='{.status.capacity.storage}'=200Mi -f files/pvc.block.resize.yaml --timeout=40s
    [ "$status" -eq 0 ]
}

@test "delete linear pod" {
    run kubectl delete -f files/pod.linear.vol.yaml --grace-period=0 --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "delete resized linear pvc" {
    run kubectl delete -f files/pvc.linear.resize.yaml --grace-period=0 --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "delete block pod" {
    run kubectl delete -f files/pod.block.vol.yaml --grace-period=0 --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "delete resized block pvc" {
    run kubectl delete -f files/pvc.block.resize.yaml --grace-period=0 --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "create storageclass mirror-integrity" {
    # Requires kernel modules:
    # modprobe dm-raid && modprobe dm-integrity
    run kubectl apply -f files/storageclass.mirror-integrity.yaml --wait --timeout=10s
    [ "$status" -eq 0 ]
}

@test "create pvc mirror-integrity" {
    run kubectl apply -f files/pvc.mirror-integrity.yaml --wait --timeout=10s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.mirror-integrity.yaml --timeout=10s
    [ "$status" -eq 0 ]
}

@test "deploy mirror-integrity pod" {
    run kubectl apply -f files/pod.mirror-integrity.vol.yaml --wait --timeout=10s
    [ "$status" -eq 0 ]
}

@test "mirror-integrity pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.mirror-integrity.vol.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "pvc mirror-integrity bound" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Bound -f files/pvc.mirror-integrity.yaml --timeout=10s
    [ "$status" -eq 0 ]
}

@test "delete mirror-integrity pod" {
    run kubectl delete -f files/pod.mirror-integrity.vol.yaml --grace-period=0 --wait --timeout=10s
    [ "$status" -eq 0 ]
}

@test "delete mirror-integrity pvc" {
    run kubectl delete -f files/pvc.mirror-integrity.yaml --grace-period=0 --wait --timeout=10s
    [ "$status" -eq 0 ]
}

@test "delete storageclass mirror-integrity" {
    run kubectl delete -f files/storageclass.mirror-integrity.yaml --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "deploy inline xfs pod with ephemeral volume" {
    run kubectl apply -f files/pod.inline.vol.xfs.yaml --wait --timeout=20s
    [ "$status" -eq 0 ]
}

@test "inline xfs pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.inline.vol.xfs.yaml --timeout=20s
    [ "$status" -eq 0 ]
}

@test "check fsType" {
    run kubectl exec -it volume-test-inline-xfs -c inline -- sh -c "mount | grep /data"
    [ "$status" -eq 0 ]
    [[ "$output" == *"xfs"* ]]
}

@test "delete inline xfs linear pod" {
    run kubectl delete -f files/pod.inline.vol.xfs.yaml --wait --grace-period=0 --timeout=20s
    [ "$status" -eq 0 ]
}

@test "deploy csi-driver-lvm eviction-controller" {
    run kubectl cordon csi-driver-lvm-worker2
    [ "$status" -eq 0 ]
    run bash -c 'kustomize build /config/default | kubectl apply -f -'
    [ "$status" -eq 0 ]
    sleep 5
    run kubectl wait -n csi-driver-lvm-controller-system --for=condition=ready pod -l app.kubernetes.io/name=csi-driver-lvm-controller --timeout=15s
    [ "$status" -eq 0 ]
    run kubectl uncordon csi-driver-lvm-worker2
    [ "$status" -eq 0 ]
}

@test "deploy csi-driver-lvm statefulset" {
    run kubectl cordon csi-driver-lvm-worker
    [ "$status" -eq 0 ]
    run kubectl apply -f files/statefulset.pvc-annotation.yaml --wait --grace-period=0 --timeout=20s
    [ "$status" -eq 0 ]
    run kubectl wait --for=condition=ready pod -l app=nginx-pvc-annotation --timeout=10s
    [ "$status" -eq 0 ]
    run kubectl apply -f files/statefulset.pod-annotation.yaml --wait --grace-period=0 --timeout=20s
    [ "$status" -eq 0 ]
    run kubectl wait --for=condition=ready pod -l app=nginx-pod-annotation --timeout=10s
    [ "$status" -eq 0 ]
    run kubectl apply -f files/statefulset.no-annotation.yaml --wait --grace-period=0 --timeout=20s
    [ "$status" -eq 0 ]
    run kubectl wait --for=condition=ready pod -l app=nginx-no-annotation --timeout=10s
    [ "$status" -eq 0 ]
    run kubectl uncordon csi-driver-lvm-worker
    [ "$status" -eq 0 ]
}

@test "drain worker node" {
    run kubectl drain csi-driver-lvm-worker2 --ignore-daemonsets
    [ "$status" -eq 0 ]

    get_pvc_selected_node() {
      local app="$1"
      kubectl get pvc -l "app=${app}" -o jsonpath='{.items[0].metadata.annotations.volume\.kubernetes\.io/selected-node}'
    }

    # wait for pvc on new node
    for i in {1..10}; do
      PVC=$(get_pvc_selected_node "nginx-pvc-annotation")
      POD=$(get_pvc_selected_node "nginx-pod-annotation")
      NOA=$(get_pvc_selected_node "nginx-no-annotation")

      if [ "$PVC" = "csi-driver-lvm-worker" ] && \
        [ "$POD" = "csi-driver-lvm-worker" ] && \
        [ "$NOA" = "csi-driver-lvm-worker2" ]; then
        break
      fi
      sleep 1
    done

    [ "$PVC" = "csi-driver-lvm-worker" ]
    [ "$POD" = "csi-driver-lvm-worker" ]
    [ "$NOA" = "csi-driver-lvm-worker2" ]
}

@test "delete csi-lvm-controller" {
    echo "⏳ Wait 10s for all PVCs to be cleaned up..." >&3
    sleep 10

    run helm uninstall --namespace csi-driver-lvm csi-driver-lvm --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete csi-driver-lvm eviction-controller" {
    run bash -c 'kustomize build /config/default | kubectl delete -f -'
    [ "$status" -eq 0 ]
}
