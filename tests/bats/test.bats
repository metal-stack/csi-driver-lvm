#!/usr/bin/env bats -p

@test "deploy csi-lvm-controller" {
    run helm install mytest --set lvm.devicePattern='/dev/loop[0-1]' --set pluginImage.pullPolicy=IfNotPresent --set provisionerImage.pullPolicy=IfNotPresent --wait /files/helm
    [ "$status" -eq 0 ]
}

@test "create pvc" {
    run kubectl apply -f /files/pvc.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "persistentvolumeclaim/lvm-pvc-block created" ]
    [ "${lines[1]}" = "persistentvolumeclaim/lvm-pvc-linear created" ]
}

@test "deploy linear pod" {
    run kubectl apply -f /files/linear.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod/volume-test created" ]
}

@test "deploy block pod" {
    run kubectl apply -f /files/block.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod/volume-test-block created" ]
}

@test "linear pvc bound" {
    run kubectl wait -n default --for=condition=ready pod/volume-test --timeout=80s
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
    run kubectl wait -n default --for=condition=ready pod/volume-test-block --timeout=40s
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
    run kubectl apply -f /files/pvc-resize.yaml
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
    run kubectl delete -f /files/linear.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod \"volume-test\" deleted" ]
}

@test "delete block pod" {
    run kubectl delete -f /files/block.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod \"volume-test-block\" deleted" ]
}

@test "delete pvc" {
    run kubectl delete -f /files/pvc.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "persistentvolumeclaim \"lvm-pvc-block\" deleted" ]
    [ "${lines[1]}" = "persistentvolumeclaim \"lvm-pvc-linear\" deleted" ]
}
