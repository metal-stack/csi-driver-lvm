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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "k8s.io/api/core/v1"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"k8s.io/klog/v2"
)

const (
	kib int64 = 1024
	mib int64 = kib * 1024
	gib int64 = mib * 1024
	tib int64 = gib * 1024
)

// Lvm contains the main parameters
type Lvm struct {
	name              string
	nodeID            string
	version           string
	endpoint          string
	ephemeral         bool
	maxVolumesPerNode int64
	devicesPattern    string
	vgName            string
	provisionerImage  string
	pullPolicy        v1.PullPolicy
	namespace         string

	ids *identityServer
	ns  *nodeServer
	cs  *controllerServer
}

var (
	vendorVersion = "dev"
)

type actionType string

type volumeAction struct {
	action           actionType
	name             string
	nodeName         string
	size             int64
	lvmType          string
	devicesPattern   string
	provisionerImage string
	pullPolicy       v1.PullPolicy
	kubeClient       kubernetes.Clientset
	namespace        string
	vgName           string
}

const (
	linearType       = "linear"
	stripedType      = "striped"
	mirrorType       = "mirror"
	actionTypeCreate = "create"
	actionTypeDelete = "delete"
	pullIfNotPresent = "ifnotpresent"
)

// NewLvmDriver creates the driver
func NewLvmDriver(driverName, nodeID, endpoint string, ephemeral bool, maxVolumesPerNode int64, version string, devicesPattern string, vgName string, namespace string, provisionerImage string, pullPolicy string) (*Lvm, error) {
	if driverName == "" {
		return nil, fmt.Errorf("no driver name provided")
	}

	if nodeID == "" {
		return nil, fmt.Errorf("no node id provided")
	}

	if endpoint == "" {
		return nil, fmt.Errorf("no driver endpoint provided")
	}
	if version != "" {
		vendorVersion = version
	}

	pp := v1.PullAlways
	if strings.ToLower(pullPolicy) == pullIfNotPresent {
		klog.Info("pullpolicy: IfNotPresent")
		pp = v1.PullIfNotPresent
	}

	klog.Infof("Driver: %v ", driverName)
	klog.Infof("Version: %s", vendorVersion)

	return &Lvm{
		name:              driverName,
		version:           vendorVersion,
		nodeID:            nodeID,
		endpoint:          endpoint,
		ephemeral:         ephemeral,
		maxVolumesPerNode: maxVolumesPerNode,
		devicesPattern:    devicesPattern,
		vgName:            vgName,
		namespace:         namespace,
		provisionerImage:  provisionerImage,
		pullPolicy:        pp,
	}, nil
}

// Run starts the lvm plugin
func (lvm *Lvm) Run() {
	// Create GRPC servers
	lvm.ids = newIdentityServer(lvm.name, lvm.version)
	lvm.ns = newNodeServer(lvm.nodeID, lvm.ephemeral, lvm.maxVolumesPerNode, lvm.devicesPattern, lvm.vgName)
	lvm.cs = newControllerServer(lvm.ephemeral, lvm.nodeID, lvm.devicesPattern, lvm.vgName, lvm.namespace, lvm.provisionerImage, lvm.pullPolicy)

	s := newNonBlockingGRPCServer()
	s.start(lvm.endpoint, lvm.ids, lvm.cs, lvm.ns)
	s.wait()
}

func mountLV(lvname, mountPath string, vgName string) (string, error) {
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

func umountLV(targetPath string) (string, error) {

	cmd := exec.Command("umount", "--lazy", "--force", targetPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Errorf("unable to umount %s output:%s err:%v", targetPath, string(out), err)
	}
	return "", nil
}

func createProvisionerPod(va volumeAction) (err error) {
	if va.name == "" || va.nodeName == "" {
		return fmt.Errorf("invalid empty name or path or node")
	}
	if va.action == actionTypeCreate && va.lvmType == "" {
		return fmt.Errorf("createlv without lvm type")
	}

	args := []string{}
	if va.action == actionTypeCreate {
		args = append(args, "createlv", "--lvsize", fmt.Sprintf("%d", va.size), "--devices", va.devicesPattern, "--lvmtype", va.lvmType)
	}
	if va.action == actionTypeDelete {
		args = append(args, "deletelv")
	}
	args = append(args, "--lvname", va.name, "--vgname", va.vgName)

	klog.Infof("start provisionerPod with args:%s", args)
	hostPathType := v1.HostPathDirectoryOrCreate
	privileged := true
	mountPropagationBidirectional := v1.MountPropagationBidirectional
	provisionerPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: string(va.action) + "-" + va.name,
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyNever,
			NodeName:      va.nodeName,
			Tolerations: []v1.Toleration{
				{
					Operator: v1.TolerationOpExists,
				},
			},
			Containers: []v1.Container{
				{
					Name:    "csi-lvmplugin-" + string(va.action),
					Image:   va.provisionerImage,
					Command: []string{"/csi-lvmplugin-provisioner"},
					Args:    args,
					VolumeMounts: []v1.VolumeMount{
						{
							Name:             "devices",
							ReadOnly:         false,
							MountPath:        "/dev",
							MountPropagation: &mountPropagationBidirectional,
						},
						{
							Name:      "modules",
							ReadOnly:  false,
							MountPath: "/lib/modules",
						},
						{
							Name:             "lvmbackup",
							ReadOnly:         false,
							MountPath:        "/etc/lvm/backup",
							MountPropagation: &mountPropagationBidirectional,
						},
						{
							Name:             "lvmcache",
							ReadOnly:         false,
							MountPath:        "/etc/lvm/cache",
							MountPropagation: &mountPropagationBidirectional,
						},
						{
							Name:             "lvmlock",
							ReadOnly:         false,
							MountPath:        "/run/lock/lvm",
							MountPropagation: &mountPropagationBidirectional,
						},
					},
					TerminationMessagePath: "/termination.log",
					ImagePullPolicy:        va.pullPolicy,
					SecurityContext: &v1.SecurityContext{
						Privileged: &privileged,
					},
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							"cpu":    resource.MustParse("50m"),
							"memory": resource.MustParse("50Mi"),
						},
						Limits: v1.ResourceList{
							"cpu":    resource.MustParse("100m"),
							"memory": resource.MustParse("100Mi"),
						},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: "devices",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/dev",
							Type: &hostPathType,
						},
					},
				},
				{
					Name: "modules",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/lib/modules",
							Type: &hostPathType,
						},
					},
				},
				{
					Name: "lvmbackup",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/etc/lvm/backup",
							Type: &hostPathType,
						},
					},
				},
				{
					Name: "lvmcache",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/etc/lvm/cache",
							Type: &hostPathType,
						},
					},
				},
				{
					Name: "lvmlock",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: "/run/lock/lvm",
							Type: &hostPathType,
						},
					},
				},
			},
		},
	}

	// If it already exists due to some previous errors, the pod will be cleaned up later automatically
	// https://github.com/rancher/local-path-provisioner/issues/27
	_, err = va.kubeClient.CoreV1().Pods(va.namespace).Create(context.Background(), provisionerPod, metav1.CreateOptions{})
	if err != nil && !k8serror.IsAlreadyExists(err) {
		return err
	}

	defer func() {
		e := va.kubeClient.CoreV1().Pods(va.namespace).Delete(context.Background(), provisionerPod.Name, metav1.DeleteOptions{})
		if e != nil {
			klog.Errorf("unable to delete the provisioner pod: %v", e)
		}
	}()

	completed := false
	retrySeconds := 60
	for i := 0; i < retrySeconds; i++ {
		pod, err := va.kubeClient.CoreV1().Pods(va.namespace).Get(context.Background(), provisionerPod.Name, metav1.GetOptions{})
		if pod.Status.Phase == v1.PodFailed {
			// pod terminated in time, but with failure
			// return ResourceExhausted so the requesting pod can be rescheduled to anonther node
			// see https://github.com/kubernetes-csi/external-provisioner/pull/405
			klog.Info("provisioner pod terminated with failure")
			return status.Error(codes.ResourceExhausted, "volume creation failed")
		}
		if err != nil {
			klog.Errorf("error reading provisioner pod:%v", err)
		} else if pod.Status.Phase == v1.PodSucceeded {
			klog.Info("provisioner pod terminated successfully")
			completed = true
			break
		}
		klog.Infof("provisioner pod status:%s", pod.Status.Phase)
		time.Sleep(1 * time.Second)
	}
	if !completed {
		return fmt.Errorf("create process timeout after %v seconds", retrySeconds)
	}

	klog.Infof("Volume %v has been %vd on %v", va.name, va.action, va.nodeName)
	return nil
}

// VgExists checks if the given volume group exists
func vgExists(vgname string) bool {
	cmd := exec.Command("vgs", vgname, "--noheadings", "-o", "vg_name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to list existing volumegroups:%v", err)
		return false
	}
	return vgname == strings.TrimSpace(string(out))
}

// VgActivate execute vgchange -ay to activate all volumes of the volume group
func vgActivate(name string) {
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
}

func devices(devicesPattern []string) (devices []string, err error) {
	for _, devicePattern := range devicesPattern {
		klog.Infof("search devices: %s ", devicePattern)
		matches, err := filepath.Glob(strings.TrimSpace(devicePattern))
		if err != nil {
			return nil, err
		}
		klog.Infof("found: %s", matches)
		devices = append(devices, matches...)
	}
	return devices, nil
}

// CreateVG creates a volume group matching the given device patterns
func CreateVG(name string, devicesPattern string) (string, error) {
	dp := strings.Split(devicesPattern, ",")
	if len(dp) == 0 {
		return name, fmt.Errorf("invalid empty flag %v", dp)
	}

	vgexists := vgExists(name)
	if vgexists {
		klog.Infof("volumegroup: %s already exists\n", name)
		return name, nil
	}
	vgActivate(name)
	// now check again for existing vg again
	vgexists = vgExists(name)
	if vgexists {
		klog.Infof("volumegroup: %s already exists\n", name)
		return name, nil
	}

	physicalVolumes, err := devices(dp)
	if err != nil {
		return "", fmt.Errorf("unable to lookup devices from devicesPattern %s, err:%v", devicesPattern, err)
	}
	tags := []string{"vg.metal-stack.io/csi-lvm-driver"}

	args := []string{"-v", name}
	args = append(args, physicalVolumes...)
	for _, tag := range tags {
		args = append(args, "--addtag", tag)
	}
	klog.Infof("create vg with command: vgcreate %v", args)
	cmd := exec.Command("vgcreate", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// CreateLVS creates the new volume
// used by lvcreate provisioner pod and by nodeserver for ephemeral volumes
func CreateLVS(ctx context.Context, vg string, name string, size uint64, lvmType string) (string, error) {

	if lvExists(vg, name) {
		klog.Infof("logicalvolume: %s already exists\n", name)
		return name, nil
	}

	if size == 0 {
		return "", fmt.Errorf("size must be greater than 0")
	}

	if !(lvmType == "linear" || lvmType == "mirror" || lvmType == "striped") {
		return "", fmt.Errorf("lvmType is incorrect: %s", lvmType)
	}

	// TODO: check available capacity, fail if request doesn't fit

	args := []string{"-v", "-n", name, "-W", "y", "-L", fmt.Sprintf("%db", size)}

	pvs, err := pvCount(vg)
	if err != nil {
		return "", fmt.Errorf("unable to determine pv count of vg: %v", err)
	}

	if pvs < 2 {
		klog.Warning("pvcount is <2 only linear is supported")
		lvmType = linearType
	}

	switch lvmType {
	case stripedType:
		args = append(args, "--type", "striped", "--stripes", fmt.Sprintf("%d", pvs))
	case mirrorType:
		args = append(args, "--type", "raid1", "--mirrors", "1", "--nosync")
	case linearType:
	default:
		return "", fmt.Errorf("unsupported lvmtype: %s", lvmType)
	}

	tags := []string{"lv.metal-stack.io/csi-lvm-driver"}
	for _, tag := range tags {
		args = append(args, "--addtag", tag)
	}
	args = append(args, vg)
	klog.Infof("lvcreate %s", args)
	cmd := exec.Command("lvcreate", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func lvExists(vg string, name string) bool {
	cmd := exec.Command("lvs", vg+"/"+name, "--noheadings", "-o", "lv_name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		klog.Infof("unable to list existing volumes:%v", err)
		return false
	}
	return name == strings.TrimSpace(string(out))
}

func extendLVS(ctx context.Context, vg string, name string, size uint64, isBlock bool) (string, error) {

	if !lvExists(vg, name) {
		return "", fmt.Errorf("logical volume %s does not exist", name)
	}

	// TODO: check available capacity, fail if request doesn't fit

	args := []string{"-L", fmt.Sprintf("%db", size)}
	if isBlock {
		args = append(args, "-n")
	} else {
		args = append(args, "-r")
	}
	args = append(args, fmt.Sprintf("%s/%s", vg, name))
	klog.Infof("lvextend %s", args)
	cmd := exec.Command("lvextend", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RemoveLVS executes lvremove
func RemoveLVS(ctx context.Context, vg string, name string) (string, error) {

	if !lvExists(vg, name) {
		return fmt.Sprintf("logical volume %s does not exist. Assuming it has already been deleted.", name), nil
	}
	args := []string{"-q", "-y"}
	args = append(args, fmt.Sprintf("%s/%s", vg, name))
	klog.Infof("lvremove %s", args)
	cmd := exec.Command("lvremove", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func pvCount(vgname string) (int, error) {
	cmd := exec.Command("vgs", vgname, "--noheadings", "-o", "pv_count")
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
