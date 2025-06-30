SHA := $(shell git rev-parse --short=8 HEAD)
GITVERSION := $(shell git describe --long --all)
BUILDDATE := $(shell date -Iseconds)
VERSION := $(or ${VERSION},$(shell git describe --tags --exact-match 2> /dev/null || git symbolic-ref -q --short HEAD || git rev-parse --short HEAD))

GO111MODULE := on
KUBECONFIG := $(shell pwd)/.kubeconfig
HELM_REPO := "https://helm.metal-stack.io"

CONTROLLER_TOOLS_VERSION ?= v0.18.0
LOCALBIN ?= $(shell pwd)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

ifeq ($(CI),true)
  DOCKER_TTY_ARG=
else
  DOCKER_TTY_ARG=t
endif

all: provisioner lvmplugin

.PHONY: lvmplugin
lvmplugin:
	go mod tidy
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o ./bin/lvmplugin ./cmd/lvmplugin
	strip ./bin/lvmplugin

.PHONY: provisioner
provisioner:
	go mod tidy
	go build -tags netgo -o bin/csi-lvmplugin-provisioner cmd/provisioner/*.go
	strip bin/csi-lvmplugin-provisioner

.PHONY: build-plugin
build-plugin:
	docker build -t csi-driver-lvm .

.PHONY: build-provisioner
build-provisioner:
	docker build -t csi-driver-lvm-provisioner . -f cmd/provisioner/Dockerfile

/dev/loop%:
	@fallocate --length 2G loop$*.img
ifndef GITHUB_ACTIONS
	@sudo mknod $@ b 7 $*
endif
	@sudo losetup $@ loop$*.img
	@sudo losetup $@

rm-loop%:
	@sudo losetup -d /dev/loop$* || true
	@! losetup /dev/loop$*
	@sudo rm -f /dev/loop$*
	@rm -f loop$*.img
# If removing this loop device fails, you may need to:
# 	sudo dmsetup info
# 	sudo dmsetup remove <DEVICE_NAME>

.PHONY: kind
kind:
	@if ! which kind > /dev/null; then echo "kind needs to be installed"; exit 1; fi
	@if ! kind get clusters | grep csi-driver-lvm > /dev/null; then \
		kind create cluster \
		  --name csi-driver-lvm \
			--config tests/kind.yaml \
			--kubeconfig $(KUBECONFIG); fi
	@kind --name csi-driver-lvm load docker-image csi-driver-lvm
	@kind --name csi-driver-lvm load docker-image csi-driver-lvm-provisioner
	@kind --name csi-driver-lvm load docker-image csi-driver-lvm-controller

.PHONY: rm-kind
rm-kind:
	@kind delete cluster --name csi-driver-lvm

RERUN ?= 1
.PHONY: test
test: build-plugin build-provisioner build-controller /dev/loop100 /dev/loop101 kind
	@cd tests && docker build -t csi-bats . && cd -
	@touch $(KUBECONFIG)
	@for i in {1..$(RERUN)}; do \
	docker run -i$(DOCKER_TTY_ARG) \
		-e HELM_REPO=$(HELM_REPO) \
		-v "$(KUBECONFIG):/root/.kube/config" \
		-v "$(PWD)/tests:/code" \
		-v "$(PWD)/config:/config" \
		--network host \
		csi-bats \
		--verbose-run --trace --timing bats/test.bats ; \
	done

.PHONY: test-cleanup
test-cleanup: rm-kind

#!
#! CONTROLLER
#!

.PHONY: controller
controller: generate fmt #vet
	CGO_ENABLED=0 go build \
		-tags netgo \
		-trimpath \
		-ldflags \
			"-X 'github.com/metal-stack/v.Version=$(VERSION)' \
			-X 'github.com/metal-stack/v.Revision=$(GITVERSION)' \
			-X 'github.com/metal-stack/v.GitSHA1=$(SHA)' \
			-X 'github.com/metal-stack/v.BuildDate=$(BUILDDATE)'" \
		-o bin/controller cmd/controller/main.go
	strip bin/controller
	sha256sum bin/controller > bin/controller.sha256

.PHONY: build-controller
build-controller:
	docker build -f Dockerfile.Controller -t csi-driver-lvm-controller .

.PHONY: manifests
manifests: controller-gen 
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

deploy: manifests
	cd config/manager && kustomize edit set image controller=csi-driver-lvm-controller
	kustomize build config/default | kubectl apply -f -
	
.PHONY: undeploy
undeploy: 
	kustomize build config/default | kubectl delete -f -

.PHONY: generate
generate: controller-gen manifests
	go generate ./...
	$(CONTROLLER_GEN) object paths="./..."

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

# .PHONY: test
# test: manifests generate fmt vet setup-envtest ## Run tests.
# 	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: setup-envtest 
setup-envtest: $(ENVTEST)
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
