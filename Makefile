GO111MODULE := on
KUBECONFIG := $(shell pwd)/.kubeconfig
HELM_REPO := "https://helm.metal-stack.io"

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

.PHONY: test
test: build-plugin build-provisioner
	@if ! which kind > /dev/null; then echo "kind needs to be installed"; exit 1; fi
	@if ! kind get clusters | grep csi-driver-lvm > /dev/null; then \
		kind create cluster \
		  --name csi-driver-lvm \
			--config tests/kind.yaml \
			--kubeconfig $(KUBECONFIG); fi
	@kind --name csi-driver-lvm load docker-image csi-driver-lvm
	@kind --name csi-driver-lvm load docker-image csi-driver-lvm-provisioner
	@cd tests && docker build -t csi-bats . && cd -
	docker run -i$(DOCKER_TTY_ARG) \
		-e HELM_REPO=$(HELM_REPO) \
		-v "$(KUBECONFIG):/root/.kube/config" \
		-v "$(PWD)/tests:/code" \
		--network host \
		csi-bats \
		--verbose-run --trace --timing bats/test.bats
