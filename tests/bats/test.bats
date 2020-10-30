#!/usr/bin/env bats -p

@test "prepare test files" {
    run sed -i "s/PRTAG/${DOCKER_TAG}/g;" /files/*.yaml
    [ "$status" -eq 0 ]
}

@test "deploy csi-lvm-controller" {
    #run helm uninstall --wait ${DOCKER_TAG} -n ${DOCKER_TAG}
    run kubectl create ns ${DOCKER_TAG}
    run helm install ${DOCKER_TAG} --wait /files/charts/csi-driver-lvm --set pluginImage.tag=${DOCKER_TAG} --set provisionerImage.tag=${DOCKER_TAG} --set lvm.devicePattern="${DEVICEPATTERN}" --set pluginImage.pullPolicy=${PULL_POLICY} --set provisionerImage.pullPolicy=${PULL_POLICY} --set lvm.driverName="${DOCKER_TAG}.lvm.csi.metal-stack.io" --set lvm.storageClassStub="${DOCKER_TAG}-csi-lvm" --set snapshots.enabled=true --set snapshots.s3Endpoint="http://minio:9000" --set snapshots.s3AccessKey=myaccesskey --set snapshots.s3SecretKey=mysecretkey --set snapshots.encryptionPassphrase=myS3cr3tEncryptionPassphrase --set snapshots.s3BucketName=my-bucket -n ${DOCKER_TAG}
    [ "$status" -eq 0 ]
}

@test "deploy minio" {
    run kubectl apply -f /files/minio-psp.yaml -n ${DOCKER_TAG}
    run helm install --repo https://helm.min.io/ -n ${DOCKER_TAG} --wait --set accessKey=myaccesskey,secretKey=mysecretkey minio minio
    [ "$status" -eq 0 ]
}

@test "deploy inline pod with ephemeral volume" {
    run sleep 10
    run kubectl apply -f /files/inline.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod/volume-test-inline created" ]
}

@test "inline pod running" {
    run kubectl wait -n ${DOCKER_TAG} --for=condition=ready pod/volume-test-inline --timeout=180s
    run kubectl get -n ${DOCKER_TAG} pods volume-test-inline -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "volume-test-inline,Running" ]
}

@test "delete inline linear pod" {
    run kubectl delete -f /files/inline.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod \"volume-test-inline\" deleted" ]
}

@test "create pvc" {
    run kubectl apply -f /files/pvc.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "persistentvolumeclaim/lvm-pvc-block created" ]
    [ "${lines[1]}" = "persistentvolumeclaim/lvm-pvc-linear created" ]
}

@test "deploy linear pod" {
    run sleep 10
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
    run kubectl wait -n ${DOCKER_TAG} --for=condition=ready pod/volume-test --timeout=180s
    run kubectl get -n ${DOCKER_TAG} pvc lvm-pvc-linear -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "lvm-pvc-linear,Bound" ]
}

@test "linear pod running" {
    run kubectl get -n ${DOCKER_TAG} pods volume-test -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "volume-test,Running" ]
}

@test "block pvc bound" {
    run kubectl wait -n ${DOCKER_TAG} --for=condition=ready pod/volume-test-block --timeout=180s
    run kubectl get -n ${DOCKER_TAG} pvc lvm-pvc-block -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "lvm-pvc-block,Bound" ]
}

@test "block pod running" {
    run kubectl get -n ${DOCKER_TAG} pods volume-test-block -o jsonpath="{.metadata.name},{.status.phase}"
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
    run kubectl get -n ${DOCKER_TAG} pvc lvm-pvc-linear -o jsonpath='{.status.capacity.storage}'
    [ "$status" -eq 0 ]
    [ "$output" = "20Mi" ]
}

@test "check new block volume size" {
    run kubectl get -n ${DOCKER_TAG} pvc lvm-pvc-block -o jsonpath='{.status.capacity.storage}'
    [ "$status" -eq 0 ]
    [ "$output" = "20Mi" ]
}

@test "create snapshot" {
    run kubectl apply -f /files/snapshot.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "volumesnapshot.snapshot.storage.k8s.io/test-snapshot created" ]
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

@test "restore pvc from snapshot" {
    run kubectl apply -f /files/restore.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "persistentvolumeclaim/lvm-pvc-linear created" ]
}

@test "deploy linear pod again" {
    run kubectl apply -f /files/linear.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod/volume-test created" ]
}

@test "linear pvc from snapshot bound" {
    run kubectl wait -n ${DOCKER_TAG} --for=condition=ready pod/volume-test --timeout=180s
    run kubectl get -n ${DOCKER_TAG} pvc lvm-pvc-linear -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "lvm-pvc-linear,Bound" ]
}

@test "linear pod running again" {
    run kubectl get -n ${DOCKER_TAG} pods volume-test -o jsonpath="{.metadata.name},{.status.phase}"
    [ "$status" -eq 0 ]
    [ "$output" = "volume-test,Running" ]
}

@test "delete linear pod again" {
    run kubectl delete -f /files/linear.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "pod \"volume-test\" deleted" ]
}

@test "delete restored pvc" {
    run kubectl delete -f /files/restore.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "persistentvolumeclaim \"lvm-pvc-linear\" deleted" ]
}

@test "delete snapshot" {
    run kubectl delete -f /files/snapshot.yaml
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "volumesnapshot.snapshot.storage.k8s.io \"test-snapshot\" deleted" ]
}

@test "clean up " {
    run helm uninstall ${DOCKER_TAG} -n ${DOCKER_TAG}
    run helm uninstall minio -n ${DOCKER_TAG}
    run sleep 30
    run kubectl delete ns ${DOCKER_TAG}
    run sleep 10
}
