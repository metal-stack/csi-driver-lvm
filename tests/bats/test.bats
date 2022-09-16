#!/usr/bin/env bats -p

@test "deploy csi-lvm-controller" {
    run helm install --repo ${HELM_REPO} csi-driver-lvm csi-driver-lvm --values values.yaml --wait
    [ "$status" -eq 0 ]
}

@test "deploy inline pod with ephemeral volume" {
    run sleep 10
    run kubectl apply -f files/inline.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod/volume-test-inline created" ]
}

@test "inline pod running" {
    run kubectl wait --for=condition=ready pod/volume-test-inline --timeout=180s
    run kubectl get pods volume-test-inline -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "volume-test-inline,Running" ]
}

@test "delete inline linear pod" {
    run kubectl delete -f files/inline.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod \"volume-test-inline\" deleted" ]
}

@test "create pvc" {
    run kubectl apply -f files/pvc.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "persistentvolumeclaim/lvm-pvc-block created" ]
    [ "${lines[1]}" = "persistentvolumeclaim/lvm-pvc-linear created" ]
}

@test "deploy linear pod" {
    run kubectl apply -f files/linear.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod/volume-test created" ]
}

@test "deploy block pod" {
    run kubectl apply -f files/block.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod/volume-test-block created" ]
}

@test "linear pvc bound" {
    run kubectl wait --for=condition=ready pod/volume-test --timeout=180s
    run kubectl get pvc lvm-pvc-linear -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "lvm-pvc-linear,Bound" ]
}

@test "linear pod running" {
    run kubectl get pods volume-test -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "volume-test,Running" ]
}

@test "block pvc bound" {
    run kubectl wait --for=condition=ready pod/volume-test-block --timeout=180s
    run kubectl get pvc lvm-pvc-block -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "lvm-pvc-block,Bound" ]
}

@test "block pod running" {
    run kubectl get pods volume-test-block -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "volume-test-block,Running" ]
}

@test "resize volume requests" {
    run kubectl apply -f files/pvc-resize.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "persistentvolumeclaim/lvm-pvc-block configured" ]
    [ "${lines[1]}" = "persistentvolumeclaim/lvm-pvc-linear configured" ]
}

@test "check new linear volume size" {
    run sleep 90
    run kubectl get pvc lvm-pvc-linear -o jsonpath='{.status.capacity.storage}'
    [ "$status" -eq 0 ]
    [ "$output" = "20Mi" ]
}

@test "check new block volume size" {
    run kubectl get pvc lvm-pvc-block -o jsonpath='{.status.capacity.storage}'
    [ "$status" -eq 0 ]
    [ "$output" = "20Mi" ]
}

@test "delete linear pod" {
    run kubectl delete -f files/linear.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod \"volume-test\" deleted" ]
}

@test "delete block pod" {
    run kubectl delete -f files/block.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod \"volume-test-block\" deleted" ]
}

@test "delete pvc" {
    run kubectl delete -f files/pvc.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "persistentvolumeclaim \"lvm-pvc-block\" deleted" ]
    [ "${lines[1]}" = "persistentvolumeclaim \"lvm-pvc-linear\" deleted" ]
}
@test "clean up " {
    run sleep 60
    run helm uninstall csi-driver-lvm
}
