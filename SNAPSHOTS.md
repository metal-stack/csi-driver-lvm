# Volume Snapshots #

Since version v1.17 Kubernetes has a beta support for VolumeSnapshots (see <https://kubernetes.io/docs/concepts/storage/volume-snapshots/>)

csi-driver-lvm provides local volumes, but there is no concept of *local* volumeSnapshots in Kubernetes.

Instead since 0.4.x csi-driver-lvm has ***beta*** support for volumeSnapshots which will be stored at a S3-compatible backend, using [restic](https://restic.net/)

## Installation ##

To use this feature, enable and edit following variables in the values.yaml

```yaml
snapshots:
  enabled: true

  ## your s3 credentials (will be content of the secret)
  s3Endpoint: https://s3gateway.example.com
  s3AccessKey: MYS3ACCESSKEY
  s3SecretKey: MYS3SECRETKEY
  s3BucketName: my-bucket

  ## passphrase with wich the backups will be encrypted
  encryptionPassphrase: myS3cr3tEncryptionPassphrase

  ## change these, if you run in problems with the default parameters

  # timeout for snapshot provisioner operations (snapshot create/restore) in seconds
  # increase, if you have very large volumes (or a low bandwith connection to the s3 endpoint)
  snapshotTimeout: 3600

  # amount (in percent) for lvm snapshots during snapshot creation
  # increase if you have volumes with high amount of write operations during snapshot creation
  lvmSnapshotBufferPercentage: 10
```

## Example basic usage ##

1. create a volume:
`kubectl apply -f examples/snapshot-test.yaml`
2. create a snapshot of that volume:
`kubectl apply -f examples/snapshot-test-create-snapshot.yaml`
3. remove the volume:
`kubectl delete -f examples/snapshot-test.yaml`
4. create a new volume from snapshot:
`kubectl delete -f examples/snapshot-test-restore.yaml`

## Example restore snapshot to a different cluster ##

To restore a snapshot to a different cluster, make sure to NOT delete the volumeSnapshot object in the old cluster before restoring to the new one, as as delete the volumeSnapshot object will obviously delete the snapshot at the S3 backend too.

1. get the volumeSnapshotContent name of your snapshot:

```text
$ kubectl get volumesnapshot
NAME            READYTOUSE   SOURCEPVC   SOURCESNAPSHOTCONTENT   RESTORESIZE   SNAPSHOTCLASS       SNAPSHOTCONTENT                                    CREATIONTIME   AGE
snapshot-demo   true         csi-pvc                             35748096      csi-driver-lvm-s3   snapcontent-14f38fa7-f564-429b-9686-2a29512c43e8   18h            18h
```

2. set snapshotHandle accordingly in `examples/snapshot-test-restore-newcluster.yaml`
3. create a new volume from snapshot:
`kubectl delete -f examples/snapshot-test-restore-newcluster.yaml`

## Using restic for external snapshot management ##

In case you want to restore a snapshot e.g. to your local disk, or delete certain snapshots manually, you can use restic.

First, you have to set the restic environment variables:

```bash
    export AWS_ACCESS_KEY_ID=myaccesskey
    export AWS_SECRET_ACCESS_KEY=mysecretkey
    export RESTIC_PASSWORD=myS3cr3tEncryptionPassphrase
```

### restic examples ###

list snapshots:
`restic  snapshots -r s3:https://s3gateway.example.com/my-bucket`

restore a snapshot to your local disk:
`restic restore latest -t /tmp/restore --tag snapshot=snapcontent-14f38fa7-f564-429b-9686-2a29512c43e8 -r s3:https://s3gateway.example.com/my-bucket`

delete an old snapshot:
`restic forget latest -l 0 --prune --tag snapshot=snapcontent-14f38fa7-f564-429b-9686-2a29512c43e8 -r s3:https://s3gateway.example.com/my-bucket`
