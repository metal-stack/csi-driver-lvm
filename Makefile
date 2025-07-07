GOOS ?= linux
GOARCH ?= amd64
GOARM ?= 
CGO_ENABLED ?= 1
BINARY_LVMPLUGIN := lvmplugin-$(GOOS)-$(GOARCH)
BINARY_PROVISIONER:= provisioner-$(GOOS)-$(GOARCH)

SHA := $(shell git rev-parse --short=8 HEAD)
GITVERSION := $(shell git describe --long --all)
BUILDDATE := $(shell date --rfc-3339=seconds)
VERSION := $(or ${VERSION},$(shell git describe --tags --exact-match 2> /dev/null || git symbolic-ref -q --short HEAD || git rev-parse --short HEAD))

GO111MODULE := on
KUBECONFIG := $(shell pwd)/.kubeconfig
HELM_REPO := "https://helm.metal-stack.io"

ifeq ($(CI),true)
  DOCKER_TTY_ARG=
else
  DOCKER_TTY_ARG=t
endif

ifeq ($(CGO_ENABLED),1)
ifeq ($(GOOS),linux)
	LINKMODE := -linkmode external -extldflags '-static -s -w'
	TAGS := -tags 'osusergo netgo static_build'
endif
endif

LINKMODE := $(LINKMODE) \
		 -X 'github.com/metal-stack/v.Version=$(VERSION)' \
		 -X 'github.com/metal-stack/v.Revision=$(GITVERSION)' \
		 -X 'github.com/metal-stack/v.GitSHA1=$(SHA)' \
		 -X 'github.com/metal-stack/v.BuildDate=$(BUILDDATE)'

all: provisioner lvmplugin

.PHONY: lvmplugin
lvmplugin:
	go mod tidy
	go build \
		$(TAGS) \
		-ldflags \
		"$(LINKMODE)" \
		-o bin/$(BINARY_LVMPLUGIN) \
		./cmd/lvmplugin 
	cd bin/ && \
	sha512sum $(BINARY_LVMPLUGIN) > $(BINARY_LVMPLUGIN).sha512

.PHONY: provisioner
provisioner:
	go mod tidy
	go build \
		$(TAGS) \
		-ldflags \
		"$(LINKMODE)" \
		-o bin/$(BINARY_PROVISIONER) \
		./cmd/provisioner
	cd bin/ && \
	sha512sum $(BINARY_PROVISIONER) > $(BINARY_PROVISIONER).sha512

.PHONY: build-plugin
build-plugin: lvmplugin
	docker build -t csi-driver-lvm -f cmd/lvmplugin/Dockerfile --build-arg BINARY=$(BINARY_LVMPLUGIN) .

.PHONY: build-provisioner
build-provisioner: provisioner
	docker build -t csi-driver-lvm-provisioner -f cmd/provisioner/Dockerfile --build-arg BINARY=$(BINARY_PROVISIONER) .

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

.PHONY: rm-kind
rm-kind:
	@kind delete cluster --name csi-driver-lvm

RERUN ?= 1
.PHONY: test
test: build-plugin build-provisioner /dev/loop100 /dev/loop101 kind
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
