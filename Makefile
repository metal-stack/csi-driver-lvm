KUBECONFIG := $(shell pwd)/.kubeconfig
HELM_REPO := "https://helm.metal-stack.io"
GOOS := linux
GOARCH := amd64
CGO_ENABLED := 1
TAGS := -tags 'netgo'

ifeq ($(CGO_ENABLED),1)
ifeq ($(GOOS),linux)
	LINKMODE := -linkmode external -extldflags '-static -s -w'
	TAGS := -tags 'osusergo netgo static_build'
endif
endif

ifeq ($(CI),true)
  DOCKER_TTY_ARG=
else
  DOCKER_TTY_ARG=t
endif

all: provisioner lvmplugin

.PHONY: lvmplugin
lvmplugin:
	go mod tidy
	go build $(TAGS) -ldflags "$(LINKMODE)" -o ./bin/lvmplugin-$(GOOS)-$(GOARCH) ./cmd/lvmplugin
	strip ./bin/lvmplugin-$(GOOS)-$(GOARCH)

.PHONY: provisioner
provisioner:
	go mod tidy
	go build $(TAGS) -ldflags "$(LINKMODE)" -o bin/csi-lvmplugin-provisioner-$(GOOS)-$(GOARCH) cmd/provisioner/*.go
	strip bin/csi-lvmplugin-provisioner-$(GOOS)-$(GOARCH)

.PHONY: build-dockerfiles
build-dockerfiles: lvmplugin provisioner
	docker build -t csi-driver-lvm -f Dockerfile.plugin --build-arg GOOS=$(GOOS) --build-arg GOARCH=$(GOARCH) .
	docker build -t csi-driver-lvm-provisioner -f Dockerfile.provisioner --build-arg GOOS=$(GOOS) --build-arg GOARCH=$(GOARCH) .

RERUN ?= 1
.PHONY: test
test: /dev/loop100 /dev/loop101 kind
	@cd tests && docker build -t csi-bats . && cd -
	@touch $(KUBECONFIG)
	@for i in {1..$(RERUN)}; do \
	docker run -i$(DOCKER_TTY_ARG) \
		-e HELM_REPO=$(HELM_REPO) \
		-v "$(KUBECONFIG):/root/.kube/config" \
		-v "$(PWD)/tests:/code" \
		--network host \
		csi-bats \
		--verbose-run --trace --timing bats/test.bats ; \
	done

.PHONY: test-cleanup
test-cleanup: rm-kind

.PHONY: kind
kind: build-dockerfiles
	@if ! which kind > /dev/null; then echo "kind needs to be installed"; exit 1; fi
	@if ! kind get clusters | grep csi-driver-lvm > /dev/null; then \
		kind create cluster \
		  --name csi-driver-lvm \
			--config tests/kind.yaml \
			--kubeconfig $(KUBECONFIG); fi
	@kind --name csi-driver-lvm load docker-image csi-driver-lvm
	@kind --name csi-driver-lvm load docker-image csi-driver-lvm-provisioner

.PHONY: rm-kind
rm-kind:
	@kind delete cluster --name csi-driver-lvm

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
