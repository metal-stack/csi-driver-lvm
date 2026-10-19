#!/usr/bin/env bats -p

@test "deploy snapshot-controller with distributed snapshotting" {
    run sh -c "kubectl kustomize 'https://github.com/kubernetes-csi/external-snapshotter/client/config/crd?ref=v8.5.0' | kubectl apply -f -"
    [ "$status" -eq 0 ]

    run sh -c "kubectl -n kube-system kustomize 'https://github.com/kubernetes-csi/external-snapshotter/deploy/kubernetes/snapshot-controller?ref=v8.5.0' | kubectl apply -f -"
    [ "$status" -eq 0 ]

    # distributed snapshotting labels each VolumeSnapshotContent with the node of its source volume
    run kubectl patch clusterrole snapshot-controller-runner --type=json \
        -p '[{"op":"add","path":"/rules/-","value":{"apiGroups":[""],"resources":["nodes"],"verbs":["get","list","watch"]}}]'
    [ "$status" -eq 0 ]
    run kubectl -n kube-system patch deploy snapshot-controller --type=json \
        -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--enable-distributed-snapshotting"}]'
    [ "$status" -eq 0 ]

    run kubectl -n kube-system rollout status deploy/snapshot-controller --timeout=120s
    [ "$status" -eq 0 ]
}

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
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.mirror-integrity.vol.yaml --timeout=45s
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

@test "create storageclass with mountOptions" {
    run kubectl apply -f files/storageclass.mountoptions.yaml --wait --timeout=10s
    [ "$status" -eq 0 ]
}

@test "create pvc with mountOptions storageclass" {
    run kubectl apply -f files/pvc.mountoptions.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.mountoptions.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "deploy mountOptions pod" {
    run kubectl apply -f files/pod.mountoptions.vol.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "mountOptions pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.mountoptions.vol.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "check mountOptions applied to volume" {
    run kubectl exec volume-test-mountoptions -- sh -c "mount | grep /data"
    [ "$status" -eq 0 ]
    [[ "$output" == *"noatime"* ]]
}

@test "delete mountOptions pod" {
    run kubectl delete -f files/pod.mountoptions.vol.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete mountOptions pvc" {
    run kubectl delete -f files/pvc.mountoptions.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete mountOptions storageclass" {
    run kubectl delete -f files/storageclass.mountoptions.yaml --wait --timeout=10s
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

## Encryption tests

@test "create encryption secret" {
    run kubectl apply -f files/secret.encryption.yaml --wait --timeout=10s
    [ "$status" -eq 0 ]
}

@test "deploy inline encrypted pod with ephemeral volume" {
    run kubectl apply -f files/pod.inline.encrypted.vol.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "inline encrypted pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.inline.encrypted.vol.yaml --timeout=60s
    [ "$status" -eq 0 ]
}

@test "inline encrypted pod can read written data" {
    run kubectl exec -t volume-test-inline-encrypted -c inline -- cat /data/test.txt
    [ "$status" -eq 0 ]
    [[ "$output" == *"ephemeral-encrypted-ok"* ]]
}

@test "ephemeral encrypted raw LV is LUKS-formatted" {
    NODE=$(kubectl get pod volume-test-inline-encrypted -o jsonpath='{.spec.nodeName}')
    PLUGIN_POD=$(kubectl get pods -n csi-driver-lvm -l app=csi-driver-lvm --field-selector "spec.nodeName=$NODE" -o jsonpath='{.items[0].metadata.name}')

    # Ephemeral LV names start with "csi-" — find the one currently active
    LV_NAME=$(kubectl exec -n csi-driver-lvm "$PLUGIN_POD" -c csi-driver-lvm -- lvs csi-lvm --noheadings -o lv_name | tr -d ' ' | grep '^csi-')
    [ -n "$LV_NAME" ]

    run kubectl exec -n csi-driver-lvm "$PLUGIN_POD" -c csi-driver-lvm -- cryptsetup isLuks "/dev/csi-lvm/$LV_NAME"
    [ "$status" -eq 0 ]
}

@test "ephemeral encrypted plaintext data not readable on raw LV" {
    NODE=$(kubectl get pod volume-test-inline-encrypted -o jsonpath='{.spec.nodeName}')
    PLUGIN_POD=$(kubectl get pods -n csi-driver-lvm -l app=csi-driver-lvm --field-selector "spec.nodeName=$NODE" -o jsonpath='{.items[0].metadata.name}')

    LV_NAME=$(kubectl exec -n csi-driver-lvm "$PLUGIN_POD" -c csi-driver-lvm -- lvs csi-lvm --noheadings -o lv_name | tr -d ' ' | grep '^csi-')
    [ -n "$LV_NAME" ]

    run kubectl exec -n csi-driver-lvm "$PLUGIN_POD" -c csi-driver-lvm -- strings "/dev/csi-lvm/$LV_NAME"
    [[ "$output" != *"ephemeral-encrypted-ok"* ]]
}

@test "delete inline encrypted pod" {
    run kubectl delete -f files/pod.inline.encrypted.vol.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "create encrypted linear pvc" {
    run kubectl apply -f files/pvc.encrypted-linear.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.encrypted-linear.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "deploy encrypted linear pod" {
    run kubectl apply -f files/pod.encrypted-linear.vol.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "encrypted linear pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.encrypted-linear.vol.yaml --timeout=60s
    [ "$status" -eq 0 ]
}

@test "encrypted linear pvc bound" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Bound -f files/pvc.encrypted-linear.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "write known data to encrypted linear volume" {
    run kubectl exec -t volume-test-encrypted -c volume-test-encrypted -- sh -c 'echo "ENCRYPTION_TEST_MARKER" > /encrypted/verify.txt && sync'
    [ "$status" -eq 0 ]
}

@test "raw LV is LUKS-formatted" {
    # Get the PV name (which is also the LV name) and the node it's on
    PV_NAME=$(kubectl get pvc lvm-pvc-encrypted-linear -o jsonpath='{.spec.volumeName}')
    NODE=$(kubectl get pod volume-test-encrypted -o jsonpath='{.spec.nodeName}')
    PLUGIN_POD=$(kubectl get pods -n csi-driver-lvm -l app=csi-driver-lvm --field-selector "spec.nodeName=$NODE" -o jsonpath='{.items[0].metadata.name}')

    run kubectl exec -n csi-driver-lvm "$PLUGIN_POD" -c csi-driver-lvm -- cryptsetup isLuks "/dev/csi-lvm/$PV_NAME"
    [ "$status" -eq 0 ]
}

@test "plaintext data not readable on raw LV" {
    PV_NAME=$(kubectl get pvc lvm-pvc-encrypted-linear -o jsonpath='{.spec.volumeName}')
    NODE=$(kubectl get pod volume-test-encrypted -o jsonpath='{.spec.nodeName}')
    PLUGIN_POD=$(kubectl get pods -n csi-driver-lvm -l app=csi-driver-lvm --field-selector "spec.nodeName=$NODE" -o jsonpath='{.items[0].metadata.name}')

    # Search for the known marker string on the raw LV — it must not appear
    run kubectl exec -n csi-driver-lvm "$PLUGIN_POD" -c csi-driver-lvm -- strings "/dev/csi-lvm/$PV_NAME"
    [[ "$output" != *"ENCRYPTION_TEST_MARKER"* ]]
}

@test "resize encrypted linear pvc" {
    run kubectl apply -f files/pvc.encrypted-linear.resize.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    # in some cases a pod restart is required
    run kubectl replace --force -f files/pod.encrypted-linear.vol.yaml --wait --timeout=50s --grace-period=0
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.capacity.storage}'=200Mi -f files/pvc.encrypted-linear.resize.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete encrypted linear pod" {
    run kubectl delete -f files/pod.encrypted-linear.vol.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete resized encrypted linear pvc" {
    run kubectl delete -f files/pvc.encrypted-linear.resize.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "create encrypted block pvc" {
    run kubectl apply -f files/pvc.encrypted-block.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.encrypted-block.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "deploy encrypted block pod" {
    run kubectl apply -f files/pod.encrypted-block.vol.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "encrypted block pod running" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.encrypted-block.vol.yaml --timeout=60s
    [ "$status" -eq 0 ]
}

@test "encrypted block pvc bound" {
    run kubectl wait --for=jsonpath='{.status.phase}'=Bound -f files/pvc.encrypted-block.yaml --timeout=30s
    [ "$status" -eq 0 ]
}

@test "resize encrypted block pvc" {
    run kubectl apply -f files/pvc.encrypted-block.resize.yaml --wait --timeout=40s
    [ "$status" -eq 0 ]

    # in some cases a pod restart is required
    run kubectl replace --force -f files/pod.encrypted-block.vol.yaml --wait --timeout=50s --grace-period=0
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.capacity.storage}'=200Mi -f files/pvc.encrypted-block.resize.yaml --timeout=90s
    [ "$status" -eq 0 ]
}

@test "delete encrypted block pod" {
    run kubectl delete -f files/pod.encrypted-block.vol.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete resized encrypted block pvc" {
    run kubectl delete -f files/pvc.encrypted-block.resize.yaml --grace-period=0 --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "write to encrypted volume and ensure data gets written" {
    run kubectl apply -f files/pvc.encrypted-linear.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Pending -f files/pvc.encrypted-linear.yaml --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl apply -f files/pod.encrypted-remount.vol.writing.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.encrypted-remount.vol.writing.yaml --timeout=60s
    [ "$status" -eq 0 ]

    sleep 2

    run kubectl exec -t volume-encrypted-writing-test -- cat /remount/output.log | grep "Happily writing encrypted"
    [ "$status" -eq 0 ]
}

@test "remount encrypted volume and ensure that data is still present" {
    run kubectl delete -f files/pod.encrypted-remount.vol.writing.yaml --wait --grace-period=0 --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl apply -f files/pod.encrypted-remount.vol.reading.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.phase}'=Running -f files/pod.encrypted-remount.vol.reading.yaml --timeout=60s
    [ "$status" -eq 0 ]

    sleep 1

    run kubectl logs volume-encrypted-reading-test | grep "Happily writing encrypted"
    [ "$status" -eq 0 ]

    run kubectl delete -f files/pod.encrypted-remount.vol.reading.yaml --wait --grace-period=0 --timeout=30s
    [ "$status" -eq 0 ]

    run kubectl delete -f files/pvc.encrypted-linear.yaml --wait --timeout=30s
    [ "$status" -eq 0 ]
}

@test "delete encryption secret" {
    run kubectl delete -f files/secret.encryption.yaml --wait --timeout=10s
    [ "$status" -eq 0 ]
}

@test "create xfs pvc for snapshots" {
    run kubectl apply -f files/storageclass.snapshot-xfs.yaml
    [ "$status" -eq 0 ]

    run kubectl apply -f files/pvc.snapshot-source.yaml -f files/pod.snapshot-source.yaml
    [ "$status" -eq 0 ]

    run kubectl wait --for=condition=Ready -f files/pod.snapshot-source.yaml --timeout=60s
    [ "$status" -eq 0 ]

    run kubectl exec volume-snapshot-source -- sh -c 'echo before-snapshot > /data/data.txt && sync'
    [ "$status" -eq 0 ]
}

@test "create volume snapshot" {
    run kubectl apply -f files/volumesnapshot.yaml
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.readyToUse}'=true -f files/volumesnapshot.yaml --timeout=60s
    [ "$status" -eq 0 ]

    # the snapshot content must be handled by the node that holds the source volume
    NODE=$(kubectl get pod volume-snapshot-source -o jsonpath='{.spec.nodeName}')
    CONTENT=$(kubectl get volumesnapshot lvm-snapshot -o jsonpath='{.status.boundVolumeSnapshotContentName}')
    MANAGED_BY=$(kubectl get volumesnapshotcontent "$CONTENT" -o jsonpath='{.metadata.labels.snapshot\.storage\.kubernetes\.io/managed-by}')
    [ "$MANAGED_BY" = "$NODE" ]
}

@test "write to source volume after snapshot" {
    run kubectl exec volume-snapshot-source -- sh -c 'echo after-snapshot > /data/data.txt && echo new > /data/new.txt && sync'
    [ "$status" -eq 0 ]
}

@test "restore snapshot into larger pvc on the same node" {
    NODE=$(kubectl get pod volume-snapshot-source -o jsonpath='{.spec.nodeName}')

    run kubectl apply -f files/pvc.snapshot-restore.yaml
    [ "$status" -eq 0 ]

    run sh -c "sed 's/SNAPSHOT_NODE/$NODE/' files/pod.snapshot-restore.yaml | kubectl apply -f -"
    [ "$status" -eq 0 ]

    run kubectl wait --for=condition=Ready pod/volume-snapshot-restore --timeout=120s
    [ "$status" -eq 0 ]
}

@test "restored volume contains snapshot-time data only" {
    run kubectl exec volume-snapshot-restore -- cat /data/data.txt
    [ "$status" -eq 0 ]
    [ "$output" = "before-snapshot" ]

    run kubectl exec volume-snapshot-restore -- test -e /data/new.txt
    [ "$status" -ne 0 ]
}

@test "restored xfs filesystem is grown and mounted next to its source" {
    # xfs refuses duplicate UUIDs unless mounted with nouuid, both pods run on the same node
    run kubectl exec volume-snapshot-restore -- sh -c 'grep " /data " /proc/mounts'
    [ "$status" -eq 0 ]
    [[ "$output" == *nouuid* ]]

    SIZE=$(kubectl exec volume-snapshot-restore -- df -Pm /data | awk 'NR==2 {print $2}')
    [ "$SIZE" -gt 400 ]
}

@test "deleting snapshot source volume is refused while the snapshot exists" {
    PV=$(kubectl get pvc lvm-pvc-snapshot-source -o jsonpath='{.spec.volumeName}')
    echo "$PV" > /tmp/snapshot_source_pv.txt

    run kubectl delete -f files/pod.snapshot-source.yaml --grace-period=0 --wait --timeout=60s
    [ "$status" -eq 0 ]
    run kubectl delete -f files/pvc.snapshot-source.yaml --wait=false
    [ "$status" -eq 0 ]

    end=$((SECONDS+60))
    while [ $SECONDS -lt $end ]; do
        EVENTS=$(kubectl get events --field-selector involvedObject.name="$PV",reason=VolumeFailedDelete -o jsonpath='{.items[*].message}')
        [[ "$EVENTS" == *"still has snapshots"* ]] && break
        sleep 2
    done
    [[ "$EVENTS" == *"still has snapshots"* ]]

    run kubectl get pv "$PV"
    [ "$status" -eq 0 ]
}

@test "delete volume snapshot releases the source volume" {
    PV=$(cat /tmp/snapshot_source_pv.txt)

    run kubectl delete -f files/volumesnapshot.yaml --wait --timeout=60s
    [ "$status" -eq 0 ]

    run kubectl wait --for=delete pv/"$PV" --timeout=120s
    [ "$status" -eq 0 ]
}

@test "delete restored volume" {
    run kubectl delete pod volume-snapshot-restore --grace-period=0 --wait --timeout=60s
    [ "$status" -eq 0 ]
    run kubectl delete -f files/pvc.snapshot-restore.yaml --wait --timeout=60s
    [ "$status" -eq 0 ]
    run kubectl delete -f files/storageclass.snapshot-xfs.yaml
    [ "$status" -eq 0 ]
}

@test "create block pvc for snapshots" {
    run kubectl apply -f files/pvc.snapshot-block-source.yaml -f files/pod.snapshot-block-source.yaml
    [ "$status" -eq 0 ]

    run kubectl wait --for=condition=Ready -f files/pod.snapshot-block-source.yaml --timeout=60s
    [ "$status" -eq 0 ]

    run kubectl exec volume-snapshot-block-source -- sh -c 'dd if=/dev/urandom of=/dev/xvda bs=1M count=50 conv=fsync'
    [ "$status" -eq 0 ]

    kubectl exec volume-snapshot-block-source -- sh -c 'head -c 104857600 /dev/xvda | md5sum' > /tmp/block_snapshot_md5.txt
    [ -s /tmp/block_snapshot_md5.txt ]
}

@test "create block volume snapshot" {
    run kubectl apply -f files/volumesnapshot.block.yaml
    [ "$status" -eq 0 ]

    run kubectl wait --for=jsonpath='{.status.readyToUse}'=true -f files/volumesnapshot.block.yaml --timeout=60s
    [ "$status" -eq 0 ]
}

@test "overwrite block source volume after snapshot" {
    run kubectl exec volume-snapshot-block-source -- sh -c 'dd if=/dev/urandom of=/dev/xvda bs=1M count=50 conv=fsync'
    [ "$status" -eq 0 ]

    SOURCE_MD5=$(kubectl exec volume-snapshot-block-source -- sh -c 'head -c 104857600 /dev/xvda | md5sum')
    [ "$SOURCE_MD5" != "$(cat /tmp/block_snapshot_md5.txt)" ]
}

@test "restore block snapshot into larger pvc" {
    NODE=$(kubectl get pod volume-snapshot-block-source -o jsonpath='{.spec.nodeName}')

    run kubectl apply -f files/pvc.snapshot-block-restore.yaml
    [ "$status" -eq 0 ]

    run sh -c "sed 's/SNAPSHOT_NODE/$NODE/' files/pod.snapshot-block-restore.yaml | kubectl apply -f -"
    [ "$status" -eq 0 ]

    run kubectl wait --for=condition=Ready pod/volume-snapshot-block-restore --timeout=120s
    [ "$status" -eq 0 ]
}

@test "restored block volume contains snapshot-time data and has the requested size" {
    RESTORED_MD5=$(kubectl exec volume-snapshot-block-restore -- sh -c 'head -c 104857600 /dev/xvda | md5sum')
    [ "$RESTORED_MD5" = "$(cat /tmp/block_snapshot_md5.txt)" ]

    run kubectl exec volume-snapshot-block-restore -- blockdev --getsize64 /dev/xvda
    [ "$status" -eq 0 ]
    [ "$output" -eq 209715200 ]
}

@test "block snapshot cannot be restored as a filesystem volume" {
    NODE=$(kubectl get pod volume-snapshot-block-source -o jsonpath='{.spec.nodeName}')

    run kubectl apply -f files/pvc.snapshot-block-to-fs.yaml
    [ "$status" -eq 0 ]
    run sh -c "sed 's/SNAPSHOT_NODE/$NODE/' files/pod.snapshot-block-to-fs.yaml | kubectl apply -f -"
    [ "$status" -eq 0 ]

    end=$((SECONDS+60))
    while [ $SECONDS -lt $end ]; do
        EVENTS=$(kubectl get events --field-selector involvedObject.name=lvm-pvc-snapshot-block-to-fs,reason=ProvisioningFailed -o jsonpath='{.items[*].message}')
        [[ "$EVENTS" == *"cannot be restored as a filesystem volume"* ]] && break
        sleep 2
    done
    [[ "$EVENTS" == *"cannot be restored as a filesystem volume"* ]]

    run kubectl get pvc lvm-pvc-snapshot-block-to-fs -o jsonpath='{.status.phase}'
    [ "$output" = "Pending" ]

    run kubectl delete pod volume-snapshot-block-to-fs --grace-period=0 --wait --timeout=60s
    [ "$status" -eq 0 ]
    run kubectl delete -f files/pvc.snapshot-block-to-fs.yaml --wait --timeout=60s
    [ "$status" -eq 0 ]
}

@test "delete block snapshot volumes" {
    run kubectl delete pod volume-snapshot-block-restore --grace-period=0 --wait --timeout=60s
    [ "$status" -eq 0 ]
    run kubectl delete -f files/pvc.snapshot-block-restore.yaml --wait --timeout=60s
    [ "$status" -eq 0 ]

    run kubectl delete -f files/volumesnapshot.block.yaml --wait --timeout=60s
    [ "$status" -eq 0 ]

    run kubectl delete -f files/pod.snapshot-block-source.yaml --grace-period=0 --wait --timeout=60s
    [ "$status" -eq 0 ]
    run kubectl delete -f files/pvc.snapshot-block-source.yaml --wait --timeout=60s
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
