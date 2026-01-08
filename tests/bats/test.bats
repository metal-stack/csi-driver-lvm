#!/usr/bin/env bats -p

@test "deploy csi-lvm-controller" {
    run kubectl create namespace csi-driver-lvm || true
    run helm upgrade --debug --install --namespace csi-driver-lvm csi-driver-lvm /charts/csi-driver-lvm --values values.yaml --wait --timeout=120s
    [ "$status" -eq 0 ]

    sleep 5
    run kubectl rollout status daemonset/csi-driver-lvm -n csi-driver-lvm --timeout=180s
    [ "$status" -eq 0 ]
}

@test "wait for 6 CSIStorageCapacity objects" {
    end=$((SECONDS+180))
    while [ $SECONDS -lt $end ]; do
        count=$(kubectl get csistoragecapacities \
            -n csi-driver-lvm \
            --no-headers 2>/dev/null | wc -l)
        if [ "$count" -ge 6 ]; then
            break
        fi
        sleep 2
    done

    [ "$count" -ge 6 ]
}

@test "record CSIStorageCapacity before pod creation" {
    export CAP_LINEAR_BEFORE=$(kubectl get csistoragecapacities -n csi-driver-lvm -o json \
        | jq -r '
            .items[]
            | select(.storageClassName == "csi-driver-lvm-linear")
            | select(.nodeTopology.matchLabels["topology.lvm.csi/node"] == "csi-driver-lvm-worker")
            | .capacity
            | sub("Mi$"; "")
        ')
    [ -n "$CAP_LINEAR_BEFORE" ]
    echo "$CAP_LINEAR_BEFORE" > /tmp/cap_linear_before.txt

    export CAP_MIRROR_BEFORE=$(kubectl get csistoragecapacities -n csi-driver-lvm -o json \
    | jq -r '
        .items[]
        | select(.storageClassName == "csi-driver-lvm-mirror")
        | select(.nodeTopology.matchLabels["topology.lvm.csi/node"] == "csi-driver-lvm-worker")
        | .capacity
        | sub("Mi$"; "")
    ')
    [ -n "$CAP_MIRROR_BEFORE" ]
    echo "$CAP_MIRROR_BEFORE" > /tmp/cap_mirror_before.txt

    (( CAP_LINEAR_BEFORE == 2 * CAP_MIRROR_BEFORE ))
}

@test "deploy inline pod with ephemeral volume" {
    run kubectl apply -f files/pod.inline.vol.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "inline pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.inline.vol.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "record CSIStorageCapacity after pod creation (wait until changed)" {
    end=$((SECONDS+60))
    CAP_LINEAR_AFTER=""
    CAP_LINEAR_BEFORE=$(cat /tmp/cap_linear_before.txt)

    while [ $SECONDS -lt $end ]; do
        CAP_LINEAR_AFTER=$(kubectl get csistoragecapacities -n csi-driver-lvm -o json \
            | jq -r '
                .items[]
                | select(.storageClassName == "csi-driver-lvm-linear")
                | select(.nodeTopology.matchLabels["topology.lvm.csi/node"] == "csi-driver-lvm-worker")
                | .capacity
                | sub("Mi$"; "")
            ')

        if [ "$CAP_LINEAR_AFTER" != "$CAP_LINEAR_BEFORE" ] && [ -n "$CAP_LINEAR_AFTER" ]; then
            echo "Capacity changed from $CAP_LINEAR_BEFORE to $CAP_LINEAR_AFTER"
            break
        fi
        sleep 2
    done

    DIFF=$(( CAP_LINEAR_BEFORE - CAP_LINEAR_AFTER ))
    [ "$DIFF" -eq 100 ]

    end=$((SECONDS+60))
    CAP_MIRROR_AFTER=""
    CAP_MIRROR_BEFORE=$(cat /tmp/cap_mirror_before.txt)

    while [ $SECONDS -lt $end ]; do
        CAP_MIRROR_AFTER=$(kubectl get csistoragecapacities -n csi-driver-lvm -o json \
            | jq -r '
                .items[]
                | select(.storageClassName == "csi-driver-lvm-mirror")
                | select(.nodeTopology.matchLabels["topology.lvm.csi/node"] == "csi-driver-lvm-worker")
                | .capacity
                | sub("Mi$"; "")
            ')

        if [ "$CAP_MIRROR_AFTER" != "$CAP_MIRROR_BEFORE" ] && [ -n "$CAP_MIRROR_AFTER" ]; then
            echo "Capacity changed from $CAP_MIRROR_BEFORE to $CAP_MIRROR_AFTER"
            break
        fi
        sleep 2
    done

    DIFF=$(( CAP_MIRROR_BEFORE - CAP_MIRROR_AFTER ))
    [ "$DIFF" -eq 50 ]

     (( CAP_LINEAR_AFTER == 2 * CAP_MIRROR_AFTER ))
}

@test "delete inline linear pod" {
    run kubectl delete -f files/pod.inline.vol.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "record CSIStorageCapacity after pod deletion (wait until changed)" {
    end=$((SECONDS+60))
    CAP_LINEAR_AFTER=""
    CAP_LINEAR_START=$(cat /tmp/cap_linear_before.txt)

    while [ $SECONDS -lt $end ]; do
        CAP_LINEAR_AFTER=$(kubectl get csistoragecapacities -n csi-driver-lvm -o json \
            | jq -r '
                .items[]
                | select(.storageClassName == "csi-driver-lvm-linear")
                | select(.nodeTopology.matchLabels["topology.lvm.csi/node"] == "csi-driver-lvm-worker")
                | .capacity
                | sub("Mi$"; "")
            ')

        if [ "$CAP_LINEAR_AFTER" == "$CAP_LINEAR_START" ] && [ -n "$CAP_LINEAR_AFTER" ]; then
            echo "Capacity changed to $CAP_LINEAR_START"
            break
        fi
        sleep 2
    done

    DIFF=$(( CAP_LINEAR_START - CAP_LINEAR_AFTER ))
    [ "$DIFF" -eq 0 ]

    end=$((SECONDS+60))
    CAP_MIRROR_AFTER=""
    CAP_MIRROR_START=$(cat /tmp/cap_mirror_before.txt)

    while [ $SECONDS -lt $end ]; do
        CAP_MIRROR_AFTER=$(kubectl get csistoragecapacities -n csi-driver-lvm -o json \
            | jq -r '
                .items[]
                | select(.storageClassName == "csi-driver-lvm-mirror")
                | select(.nodeTopology.matchLabels["topology.lvm.csi/node"] == "csi-driver-lvm-worker")
                | .capacity
                | sub("Mi$"; "")
            ')

        if [ "$CAP_MIRROR_AFTER" == "$CAP_MIRROR_START" ] && [ -n "$CAP_MIRROR_AFTER" ]; then
            echo "Capacity changed to $CAP_MIRROR_START"
            break
        fi
        sleep 2
    done

    DIFF=$(( CAP_MIRROR_START - CAP_MIRROR_AFTER ))
    [ "$DIFF" -eq 0 ]
}

@test "create pvc linear" {
    run kubectl apply -f files/pvc.linear.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.linear.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "deploy linear pod" {
    run kubectl apply -f files/pod.linear.vol.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "linear pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.linear.vol.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "pvc linear bound" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Bound -f files/pvc.linear.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "resize linear pvc" {
    run kubectl apply -f files/pvc.linear.resize.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    # in some cases a pod restart is required
    run kubectl replace --force -f files/pod.linear.vol.yaml --wait --timeout=50s --grace-period=0
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.capacity.storage}'=200Mi -f files/pvc.linear.resize.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "create block pvc" {
    run kubectl apply -f files/pvc.block.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.block.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "deploy block pod" {
    run kubectl apply -f files/pod.block.vol.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "block pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.block.vol.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "pvc block bound" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Bound -f files/pvc.block.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "resize block pvc" {
    run kubectl apply -f files/pvc.block.resize.yaml --wait --timeout=40s
    [ "$status" -eq 0 ]

    # in some cases a pod restart is required
    run kubectl replace --force -f files/pod.block.vol.yaml --wait --timeout=50s --grace-period=0
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.capacity.storage}'=200Mi -f files/pvc.block.resize.yaml --timeout=40s
    [ "$status" -eq 0 ]
}

@test "delete linear pod" {
    run kubectl delete -f files/pod.linear.vol.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete resized linear pvc" {
    run kubectl delete -f files/pvc.linear.resize.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete block pod" {
    run kubectl delete -f files/pod.block.vol.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete resized block pvc" {
    run kubectl delete -f files/pvc.block.resize.yaml --grace-period=0 --wait --timeout=30s
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
    run kubectl delete -f files/storageclass.mirror-integrity.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "deploy inline xfs pod with ephemeral volume" {
    run kubectl apply -f files/pod.inline.vol.xfs.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "inline xfs pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.inline.vol.xfs.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "check fsType" {
    run kubectl exec -it volume-test-inline-xfs -c inline -- sh -c "mount | grep /data"
    [ "$status" -eq 0 ]
    [[ "$output" == *"xfs"* ]]
}

@test "delete inline xfs linear pod" {
    run kubectl delete -f files/pod.inline.vol.xfs.yaml --wait --grace-period=0 --timeout=30s
    [ "$status" -eq 0 ]
}

@test "write to volume and ensure data gets written" {
    run kubectl apply -f files/pvc.remount.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.remount.yaml --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl apply -f files/pod.remount.vol.writing.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.remount.vol.writing.yaml --timeout=30s
    [ "$status" -eq 0 ]

    sleep 2

    run kubectl exec -t volume-writing-test -- cat /remount/output.log | grep "Happily writing"
    [ "$status" -eq 0 ]
}

@test "remount and ensure that data is still present" {
    run kubectl delete -f files/pod.remount.vol.writing.yaml --wait --grace-period=0 --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl apply -f files/pod.remount.vol.reading.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.remount.vol.reading.yaml --timeout=30s
    [ "$status" -eq 0 ]

    sleep 1

    run kubectl logs volume-reading-test | grep "Happily writing"
    [ "$status" -eq 0 ]

    run kubectl delete -f files/pod.remount.vol.reading.yaml --wait --grace-period=0 --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl delete -f files/pvc.remount.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "deploy csi-driver-lvm eviction-controller" {
    run kubectl cordon csi-driver-lvm-worker2
    [ "$status" -eq 0 ]

   run helm upgrade --debug --install --namespace csi-driver-lvm csi-driver-lvm /charts/csi-driver-lvm --values values.yaml --set evictionEnabled='true' --wait --timeout=120s
    [ "$status" -eq 0 ]

    sleep 5
    run kubectl rollout status daemonset/csi-driver-lvm -n csi-driver-lvm --timeout=180s
    [ "$status" -eq 0 ]

    run kubectl wait -n csi-driver-lvm --for=condition=ready pod -l app=csi-driver-lvm-controller --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl uncordon csi-driver-lvm-worker2
    [ "$status" -eq 0 ]
}

@test "deploy csi-driver-lvm statefulset" {
    run kubectl cordon csi-driver-lvm-worker
    [ "$status" -eq 0 ]
    run kubectl apply -f files/statefulset.pvc-annotation.yaml --wait --grace-period=0 --timeout=40s
    [ "$status" -eq 0 ]
    run kubectl wait --for=condition=ready pod -l app=nginx-pvc-annotation --timeout=30s
    [ "$status" -eq 0 ]
    run kubectl apply -f files/statefulset.pod-annotation.yaml --wait --grace-period=0 --timeout=40s
    [ "$status" -eq 0 ]
    run kubectl wait --for=condition=ready pod -l app=nginx-pod-annotation --timeout=30s
    [ "$status" -eq 0 ]
    run kubectl apply -f files/statefulset.no-annotation.yaml --wait --grace-period=0 --timeout=40s
    [ "$status" -eq 0 ]
    run kubectl wait --for=condition=ready pod -l app=nginx-no-annotation --timeout=30s
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

@test "cleanup csi-driver-lvm eviction" {
    run kubectl delete -f files/statefulset.pvc-annotation.yaml --wait --grace-period=0 --timeout=30s
    [ "$status" -eq 0 ]
    run kubectl delete -f files/statefulset.pod-annotation.yaml --wait --grace-period=0 --timeout=30s
    [ "$status" -eq 0 ]
    run kubectl delete -f files/statefulset.no-annotation.yaml --wait --grace-period=0 --timeout=30s
    [ "$status" -eq 0 ]
    # cleanup pvc in default ns
    run kubectl delete pvc --all
    [ "$status" -eq 0 ]

    run kubectl uncordon csi-driver-lvm-worker2
    [ "$status" -eq 0 ]
}

@test "delete csi-lvm-controller" {
    echo "⏳ Wait 10s for all PVCs to be cleaned up..." >&3
    sleep 10

    run helm uninstall --namespace csi-driver-lvm csi-driver-lvm --wait --timeout=30s
    [ "$status" -eq 0 ]
}
