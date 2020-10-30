package lvm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"k8s.io/klog/v2"
)

type resticSnapshot struct {
	Time     time.Time `json:"time"`
	Tree     string    `json:"tree"`
	Paths    []string  `json:"paths"`
	Hostname string    `json:"hostname"`
	Username string    `json:"username"`
	UID      int       `json:"uid"`
	Gid      int       `json:"gid"`
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Parent   string    `json:"parent,omitempty"`
	Tags     []string  `json:"tags,omitempty"`
}
type resticStats struct {
	TotalSize      int64 `json:"total_size"`
	TotalFileCount int64 `json:"total_file_count"`
}

type s3Snapshot struct {
	Time         time.Time `json:"time"`
	ID           string    `json:"id"`
	Size         int64     `json:"size"`
	SnapshotName string    `json:"snapshotname"`
	VolumeName   string    `json:"volumename"`
}

// S3Parameter contains all parameters needed for a s3 backend
type S3Parameter struct {
	Endpoint   string `json:"endpoint"`
	AccessKey  string `json:"accesskey"`
	SecretKey  string `json:"secretkey"`
	CryptKey   string `json:"cryptkey"`
	BucketName string `json:"bucketname"`
}

// CreateS3Snapshot creates a new backup snapshot
func CreateS3Snapshot(vg string, lv string, snapshotName string, size uint64, s3 S3Parameter, lvmSnapshotBufferPercentage int) (string, error) {
	// check if we have to initialize restic
	args := []string{"stats"}
	_, err := execResticCmd("", s3, args...)
	if err != nil {
		klog.Infof("first snapshot ever, initializing")
		args := []string{"init"}
		out, err := execResticCmd("", s3, args...)
		if err != nil {
			return "", fmt.Errorf("failed to init snapshots: %s %s", err, out)
		}
	}

	mountPath := "/tmp/snapshots/" + lv

	// lvm: Names starting "snapshot" are reserved.
	snapLv := "s-" + snapshotName

	defer func() {
		cmdout, err := umountLV(mountPath)
		if err != nil {
			klog.Errorf("unable to umount directory %s for snapshot:%s err:%v %s", mountPath, snapshotName, err, cmdout)
		}
		out, err := DeleteLVMSnapshot(vg, snapLv)
		if err != nil {
			klog.Errorf("unable to remove snapshot directory %s for snapshot:%s err:%v %s", mountPath, snapshotName, err, out)
		}
	}()

	if s3SnapshotExists(snapshotName, s3) {
		return "", fmt.Errorf("a snapshot with name %s already exists", snapshotName)
	}

	// lvm: Names starting "snapshot" are reserved.
	out, err := CreateLVMSnapshot(vg, lv, snapLv, uint64(float64(size)*float64(lvmSnapshotBufferPercentage)/100))
	if err != nil {
		return out, err
	}
	cmdout, err := mountLV(snapLv, mountPath, vg)
	if err != nil {
		mountOutput := string(cmdout)
		if !strings.Contains(mountOutput, "already mounted") {
			return string(cmdout), fmt.Errorf("unable to mount %s to %s err:%v output:%s", snapLv, mountPath, err, cmdout)
		}
	}

	// backup snap logical volume
	args = []string{"backup"}
	args = append(args, ".")
	args = append(args, "--tag", fmt.Sprintf("snapshot=%s", snapshotName))
	args = append(args, "--tag", fmt.Sprintf("volume=%s", lv))

	out, err = execResticCmd(mountPath, s3, args...)
	klog.Infof("restic output: %s", out)
	if err != nil {
		return "", err
	}
	//umount, rmdir and lvremove are handled by defer func above
	return fmt.Sprintf("snapshot %s for volume %s successfully created", snapshotName, lv), nil
}

// RestoreS3Snapshot creates a new backup snapshot
func RestoreS3Snapshot(vg string, lv string, snapshotName string, s3 S3Parameter) (string, error) {
	if !s3SnapshotExists(snapshotName, s3) {
		return "", fmt.Errorf("snapshot %s does not exist", snapshotName)
	}

	restorePath := "/tmp/restore/" + lv
	output, err := mountLV(lv, restorePath, vg)
	if err != nil {
		return "", fmt.Errorf("unable to mount lv: %v output:%s", err, output)
	}
	klog.Infof("%s mounted at %s", lv, restorePath)

	defer func() {
		out, err := umountLV(restorePath)
		if err != nil {
			klog.Errorf("unable to umount directory %s for snapshot:%s err:%v %s", restorePath, snapshotName, err, out)
		}
	}()

	args := []string{"restore", "latest"}
	args = append(args, "--tag", fmt.Sprintf("snapshot=%s", snapshotName))
	args = append(args, "--target", ".")

	out, err := execResticCmd(restorePath, s3, args...)
	klog.Infof("restic output: %s", out)
	if err != nil {
		return out, err
	}
	return fmt.Sprintf("snapshot %s successfully restored to %s", snapshotName, lv), nil
}

// DeleteS3Snapshot removes a given snapshot at the s3-backend
func DeleteS3Snapshot(snapshotName string, s3 S3Parameter) (string, error) {

	snapshots, err := s3ListSnapshots(snapshotName, "", s3)
	if err != nil {
		return "", err
	}
	if len(snapshots) == 0 {
		return fmt.Sprintf("snapshot %s not found. Already gone?", snapshotName), nil
	}
	args := []string{"forget"}
	args = append(args, "-l", "0", "--prune", "--no-lock", snapshots[0].ID)

	out, err := execResticCmd("", s3, args...)
	klog.Infof("restic output: %s", out)
	if err != nil {
		return out, err
	}
	return fmt.Sprintf("snapshot %s successfully deleted", snapshotName), nil

}

func s3SnapshotExists(snapshotName string, s3 S3Parameter) bool {
	args := []string{"snapshots"}
	args = append(args, "--tag", fmt.Sprintf("snapshot=%s", snapshotName))

	out, err := execResticCmd("", s3, args...)
	if err != nil {
		return false
	}
	if len(out) < len(snapshotName) {
		return false
	}
	return true
}

func s3ListSnapshots(snapshotName string, lv string, s3 S3Parameter) ([]s3Snapshot, error) {
	args := []string{"snapshots"}

	// filter for snapshotName and/or lv name
	if snapshotName != "" {
		args = append(args, "--tag", fmt.Sprintf("snapshot=%s", snapshotName))
	}
	if lv != "" {
		args = append(args, "--tag", fmt.Sprintf("volume=%s", lv))
	}

	snapshotsOut, err := execResticCmd("", s3, args...)
	if err != nil {
		return nil, err
	}

	var rsl []resticSnapshot
	if err := json.Unmarshal([]byte(snapshotsOut), &rsl); err != nil {
		return nil, err
	}

	var sl []s3Snapshot

	for _, rs := range rsl {
		args = []string{"stats", rs.ShortID}
		statsOut, err := execResticCmd("", s3, args...)
		if err != nil {
			return nil, err
		}
		// get stats for snapshot size
		var rStats resticStats
		if err := json.Unmarshal([]byte(statsOut), &rStats); err != nil {
			return nil, err
		}

		tags := make(map[string]string)
		for _, t := range rs.Tags {
			tp := strings.Split(t, "=")
			if len(tp) > 1 {
				tags[tp[0]] = tp[1]
			}
		}
		sn, ok := tags["snapshot"]
		if !ok {
			continue
		}
		vn, ok := tags["volume"]
		if !ok {
			continue
		}

		s := s3Snapshot{
			SnapshotName: sn,
			VolumeName:   vn,
			ID:           rs.ID,
			Time:         rs.Time,
			Size:         rStats.TotalSize,
		}
		sl = append(sl, s)
	}
	return sl, nil
}

func secretsToS3Parameter(secrets map[string]string) (S3Parameter, error) {
	endpoint, ok := secrets["s3_endpoint"]
	if !ok {
		return S3Parameter{}, fmt.Errorf("\"s3_endpoint\" is missing in snapshot secrets")
	}
	cryptKey, ok := secrets["encryption_passphrase"]
	if !ok {
		return S3Parameter{}, fmt.Errorf("\"encryption_passphrase\" is missing in snapshot secrets")
	}

	accessKey, ok := secrets["s3_access_key"]
	if !ok {
		return S3Parameter{}, fmt.Errorf("\"s3_access_key\" is missing in snapshot secrets")
	}

	secretKey, ok := secrets["s3_secret_key"]
	if !ok {
		return S3Parameter{}, fmt.Errorf("\"s3_secret_key\" is missing in snapshot secrets")
	}

	bucketName, ok := secrets["s3_bucket_name"]
	if !ok {
		return S3Parameter{}, fmt.Errorf("\"s3_bucket_name\" is missing in snapshot secrets")
	}

	return S3Parameter{
		Endpoint:   endpoint,
		CryptKey:   cryptKey,
		AccessKey:  accessKey,
		SecretKey:  secretKey,
		BucketName: bucketName,
	}, nil
}

func execResticCmd(path string, s3 S3Parameter, args ...string) (string, error) {

	args = append(args, "-r", fmt.Sprintf("s3:%s/%s", s3.Endpoint, s3.BucketName), "--json")
	// restic init has no "--host"
	if args[0] != "init" {
		args = append(args, "--host", "cluster")
	}
	cmd := exec.Command("restic", args...)

	// chdir to path
	if path != "" {
		cmd.Dir = path
	}

	// set restic env variables
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", "AWS_ACCESS_KEY_ID", s3.AccessKey),
		fmt.Sprintf("%s=%s", "AWS_SECRET_ACCESS_KEY", s3.SecretKey),
		fmt.Sprintf("%s=%s", "RESTIC_PASSWORD", s3.CryptKey))

	klog.Infof("restic %s\n", args)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// EncodeS3Parameter a s3Parameter struct to a base64-encoded json-string
func EncodeS3Parameter(s3 S3Parameter) string {
	b, err := json.Marshal(s3)
	if err != nil {
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(b)
}

// DecodeS3Parameter converts a given base64-encoded json-string back to s3Parameter struc
func DecodeS3Parameter(s3string string) (S3Parameter, error) {
	var s3 S3Parameter

	s, err := base64.RawStdEncoding.DecodeString(s3string)
	if err != nil {
		return S3Parameter{}, err
	}

	if err := json.Unmarshal(s, &s3); err != nil {
		return S3Parameter{}, err
	}
	return s3, nil
}
