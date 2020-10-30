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

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path"

	"github.com/metal-stack/csi-driver-lvm/pkg/lvm"
)

func init() {
	err := flag.Set("logtostderr", "true")
	if err != nil {
		log.Printf("unable to configure logging to stdout:%v\n", err)
	}
}

var (
	endpoint                    = flag.String("endpoint", "unix://tmp/csi.sock", "CSI endpoint")
	driverName                  = flag.String("drivername", "lvm.csi.metal-stack.io", "name of the driver")
	nodeID                      = flag.String("nodeid", "", "node id")
	ephemeral                   = flag.Bool("ephemeral", false, "publish volumes in ephemeral mode even if kubelet did not ask for it (only needed for Kubernetes 1.15)")
	showVersion                 = flag.Bool("version", false, "Show version.")
	devicesPattern              = flag.String("devices", "", "comma-separated grok patterns of the physical volumes to use.")
	vgName                      = flag.String("vgname", "csi-lvm", "name of volume group")
	namespace                   = flag.String("namespace", "csi-lvm", "name of namespace")
	provisionerImage            = flag.String("provisionerimage", "metalstack/csi-lvmplugin-provisioner", "name of provisioner image")
	pullPolicy                  = flag.String("pullpolicy", "ifnotpresent", "pull policy for provisioner image")
	lvmTimeout                  = flag.Int("lvm-timeout", 60, "timeout for lvm provisioner operations (lvcreate/lvremove)")
	snapshotTimeout             = flag.Int("snapshot-timeout", 3600, "timeout for snapshot provisioner operations (snapshot create/restore")
	lvmSnapshotBufferPercentage = flag.Int("lvm-snapshot-buffer-percentage", 10, "amount (in percent) for lvm snapshots during snapshot creation")

	// Set by the build process
	version = ""
)

func main() {
	flag.Parse()

	if *showVersion {
		baseName := path.Base(os.Args[0])
		fmt.Println(baseName, version)
		return
	}

	if *ephemeral {
		fmt.Fprintln(os.Stderr, "Deprecation warning: The ephemeral flag is deprecated and should only be used when deploying on Kubernetes 1.15. It will be removed in the future.")
	}

	handle()
	os.Exit(0)
}

func handle() {
	driver, err := lvm.NewLvmDriver(*driverName, *nodeID, *endpoint, *ephemeral, version, *devicesPattern, *vgName, *namespace, *provisionerImage, *pullPolicy, *lvmTimeout, *snapshotTimeout, *lvmSnapshotBufferPercentage)
	if err != nil {
		fmt.Printf("Failed to initialize driver: %s", err.Error())
		os.Exit(1)
	}
	driver.Run()
}
