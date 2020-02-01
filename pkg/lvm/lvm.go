/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package lvm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/golang/glog"

	"github.com/google/lvmd/commands"
	"k8s.io/klog"
)

const (
	kib    int64 = 1024
	mib    int64 = kib * 1024
	gib    int64 = mib * 1024
	gib100 int64 = gib * 100
	tib    int64 = gib * 1024
	tib100 int64 = tib * 100
)

type lvm struct {
	name              string
	nodeID            string
	version           string
	endpoint          string
	ephemeral         bool
	maxVolumesPerNode int64
	devicesPattern    string
	vgName            string

	ids *identityServer
	ns  *nodeServer
	cs  *controllerServer
}

var (
	vendorVersion = "dev"
)

func NewLvmDriver(driverName, nodeID, endpoint string, ephemeral bool, maxVolumesPerNode int64, version string, devicesPattern string, vgName string) (*lvm, error) {
	if driverName == "" {
		return nil, fmt.Errorf("No driver name provided")
	}

	if nodeID == "" {
		return nil, fmt.Errorf("No node id provided")
	}

	if endpoint == "" {
		return nil, fmt.Errorf("No driver endpoint provided")
	}
	if version != "" {
		vendorVersion = version
	}

	glog.Infof("Driver: %v ", driverName)
	glog.Infof("Version: %s", vendorVersion)

	return &lvm{
		name:              driverName,
		version:           vendorVersion,
		nodeID:            nodeID,
		endpoint:          endpoint,
		ephemeral:         ephemeral,
		maxVolumesPerNode: maxVolumesPerNode,
		devicesPattern:    devicesPattern,
		vgName:            vgName,
	}, nil
}

func (lvm *lvm) Run() {
	// Create GRPC servers
	lvm.ids = NewIdentityServer(lvm.name, lvm.version)
	lvm.ns = NewNodeServer(lvm.nodeID, lvm.ephemeral, lvm.maxVolumesPerNode, lvm.devicesPattern, lvm.vgName)
	lvm.cs = NewControllerServer(lvm.ephemeral, lvm.nodeID, lvm.devicesPattern, lvm.vgName)

	s := NewNonBlockingGRPCServer()
	s.Start(lvm.endpoint, lvm.ids, lvm.cs, lvm.ns)
	s.Wait()
}

// deleteVolume deletes the directory for the lvm volume.
func deleteLvmVolume(lvName string, vgName string) error {
	glog.V(4).Infof("deleting lvm volume: %s", lvName)

	output, err := commands.RemoveLV(context.Background(), vgName, lvName)
	if err != nil {
		return fmt.Errorf("unable to delete lv: %v output:%s", err, output)
	}
	klog.Infof("lv %s vg:%s deleted", lvName, vgName)

	return nil
}

// lvmIsEmpty is a simple check to determine if the specified lvm directory
// is empty or not.
func lvmIsEmpty(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return true, fmt.Errorf("unable to open lvm volume, error: %v", err)
	}
	defer f.Close()

	_, err = f.Readdir(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

func createLvmVolume(volID, lvName string, lvSize int64, volAccessType accessType, ephemeral bool, devicesPattern string, lvmType string, vgName string) error {

	klog.Infof("create lv %s size:%d vg:%s devicespattern:%s dir:%s type:%s ", lvName, lvSize, vgName, devicesPattern, volID, lvmType)

	output, err := createVG(vgName, devicesPattern)
	if err != nil {
		return fmt.Errorf("unable to create vg: %v output:%s", err, output)
	}

	output, err = createLVS(context.Background(), vgName, lvName, lvSize, lvmType)
	if err != nil {
		return fmt.Errorf("unable to create lv: %v output:%s", err, output)
	}
	return nil
}

func devices(devicesPattern string) (devices []string, err error) {
	klog.Infof("search devices :%s ", devicesPattern)
	matches, err := filepath.Glob(devicesPattern)
	if err != nil {
		return nil, err
	}
	klog.Infof("found: %s", matches)
	return matches, nil
}

func mountLV(lvname, mountPath string, vgName string) (string, error) {
	// check for format with blkid /dev/csi-lvm/pvc-xxxxx
	// /dev/dm-3: UUID="d1910e3a-32a9-48d2-aa2e-e5ad018237c9" TYPE="ext4"
	lvPath := fmt.Sprintf("/dev/%s/%s", vgName, lvname)

	formatted := false
	// check for already formatted
	cmd := exec.Command("blkid", lvPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to check if %s is already formatted:%v", lvPath, err)
	}
	if strings.Contains(string(out), "ext4") {
		formatted = true
	}

	if !formatted {
		klog.Infof("formatting with mkfs.ext4 %s", lvPath)
		cmd = exec.Command("mkfs.ext4", lvPath)
		out, err = cmd.CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("unable to format lv:%s err:%v", lvname, err)
		}
	}

	err = os.MkdirAll(mountPath, 0777)
	if err != nil {
		return string(out), fmt.Errorf("unable to create mount directory for lv:%s err:%v", lvname, err)
	}

	// --make-shared is required that this mount is visible outside this container.
	mountArgs := []string{"--make-shared", "-t", "ext4", lvPath, mountPath}
	klog.Infof("mountlv command: mount %s", mountArgs)
	cmd = exec.Command("mount", mountArgs...)
	out, err = cmd.CombinedOutput()
	if err != nil {
		mountOutput := string(out)
		if !strings.Contains(mountOutput, "already mounted") {
			return string(out), fmt.Errorf("unable to mount %s to %s err:%v output:%s", lvPath, mountPath, err, out)
		}
	}
	err = os.Chmod(mountPath, 0777)
	if err != nil {
		return "", fmt.Errorf("unable to change permissions of volume mount %s err:%v", mountPath, err)
	}
	klog.Infof("mountlv output:%s", out)
	return "", nil
}

func bindMountLV(lvname, mountPath string, vgName string) (string, error) {
	lvPath := fmt.Sprintf("/dev/%s/%s", vgName, lvname)
	_, err := os.Create(mountPath)
	if err != nil {
		return "", fmt.Errorf("unable to create mount directory for lv:%s err:%v", lvname, err)
	}

	// --make-shared is required that this mount is visible outside this container.
	// --bind is required for raw block volumes to make them visible inside the pod.
	mountArgs := []string{"--make-shared", "--bind", lvPath, mountPath}
	klog.Infof("bindmountlv command: mount %s", mountArgs)
	cmd := exec.Command("mount", mountArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		mountOutput := string(out)
		if !strings.Contains(mountOutput, "already mounted") {
			return string(out), fmt.Errorf("unable to mount %s to %s err:%v output:%s", lvPath, mountPath, err, out)
		}
	}
	err = os.Chmod(mountPath, 0777)
	if err != nil {
		return "", fmt.Errorf("unable to change permissions of volume mount %s err:%v", mountPath, err)
	}
	klog.Infof("bindmountlv output:%s", out)
	return "", nil
}

func vgExists(name string) bool {
	vgs, err := commands.ListVG(context.Background())
	if err != nil {
		klog.Infof("unable to list existing volumegroups:%v", err)
	}
	vgexists := false
	for _, vg := range vgs {
		klog.Infof("compare vg:%s with:%s\n", vg.Name, name)
		if vg.Name == name {
			vgexists = true
			break
		}
	}
	return vgexists
}

func createVG(name string, devicesPattern string) (string, error) {
	vgexists := vgExists(name)
	if vgexists {
		klog.Infof("volumegroup: %s already exists\n", name)
		return name, nil
	} else {
		// scan for vgs and activate if any
		cmd := exec.Command("vgscan")
		out, err := cmd.CombinedOutput()
		if err != nil {
			klog.Infof("unable to scan for volumegroups:%s %v", out, err)
		}
		cmd = exec.Command("vgchange", "-ay")
		_, err = cmd.CombinedOutput()
		if err != nil {
			klog.Infof("unable to activate volumegroups:%s %v", out, err)
		}
		// now check again for existing vg again
		vgexists = vgExists(name)
		if vgexists {
			klog.Infof("volumegroup: %s already exists\n", name)
			return name, nil
		}
	}

	physicalVolumes, err := devices(devicesPattern)
	if err != nil {
		return "", fmt.Errorf("unable to lookup devices from devicesPattern %s, err:%v", devicesPattern, err)
	}
	tags := []string{"vg.metal-pod.io/csi-lvm"}

	args := []string{"-v", name}
	args = append(args, physicalVolumes...)
	for _, tag := range tags {
		args = append(args, "--add-tag", tag)
	}
	klog.Infof("create vg with command: vgcreate %v", args)
	cmd := exec.Command("vgcreate", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// createLV creates a new volume
func createLVS(ctx context.Context, vg string, name string, size int64, lvmType string) (string, error) {
	lvs, err := commands.ListLV(context.Background(), vg+"/"+name)
	if err != nil {
		klog.Infof("unable to list existing logicalvolumes:%v", err)
	}
	lvExists := false
	for _, lv := range lvs {
		klog.Infof("compare lv:%s with:%s\n", lv.Name, name)
		if strings.Contains(lv.Name, name) {
			lvExists = true
			break
		}
	}

	if lvExists {
		klog.Infof("logicalvolume: %s already exists\n", name)
		return name, nil
	}

	if size == 0 {
		return "", fmt.Errorf("size must be greater than 0")
	}

	args := []string{"-v", "-n", name, "-W", "y", "-L", fmt.Sprintf("%db", size)}

	pvs, err := pvCount(vg)
	if err != nil {
		return "", fmt.Errorf("unable to determine pv count of vg: %v", err)
	}

	if pvs < 2 {
		klog.Warning("pvcount is <2 only linear is supported")
		lvmType = "linear"
	}

	switch lvmType {
	case "striped":
		args = append(args, "--type", "striped", "--stripes", fmt.Sprintf("%d", pvs))
	case "mirror":
		args = append(args, "--type", "raid1", "--mirrors", "1", "--nosync")
	case "linear":
	default:
		return "", fmt.Errorf("unsupported lvmtype: %s", lvmType)
	}

	tags := []string{"lv.metal-pod.io/csi-lvm"}
	for _, tag := range tags {
		args = append(args, "--add-tag", tag)
	}
	args = append(args, vg)
	klog.Infof("lvreate %s", args)
	cmd := exec.Command("lvcreate", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func pvCount(vgName string) (int, error) {
	cmd := exec.Command("vgs", vgName, "--noheadings", "-o", "pv_count")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, err
	}
	outStr := strings.TrimSpace(string(out))
	count, err := strconv.Atoi(outStr)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func umountLV(lvName string, vgName string) (string, error) {

	lvPath := fmt.Sprintf("/dev/%s/%s", vgName, lvName)

	cmd := exec.Command("umount", "--lazy", "--force", lvPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Errorf("unable to umount %s from %s output:%s err:%v", lvPath, string(out), err)
	}
	return "", nil
}
