GO111MODULE := on
KUBECONFIG := $(shell pwd)/.kubeconfig
DOCKER_TAG := $(or $(GITHUB_TAG_NAME),latest)
HELM_REPO := "https://helm.metal-stack.io"

all: provisioner lvmplugin

.PHONY: lvmplugin
lvmplugin:
	go mod tidy
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o ./bin/lvmplugin ./cmd/lvmplugin
	strip ./bin/lvmplugin

.PHONY: provisioner
provisioner:
	go build -tags netgo -o bin/csi-lvmplugin-provisioner cmd/provisioner/*.go
	strip bin/csi-lvmplugin-provisioner

.PHONY: test
test:
	@if ! which kind > /dev/null; then echo "kind needs to be installed"; exit 1; fi
	@if ! kind get clusters | grep csi-driver-lvm > /dev/null; then \
		kind create cluster \
		  --name csi-driver-lvm \
			--config tests/kind.yaml \
			--kubeconfig $(KUBECONFIG); fi
	@cd tests && docker build -t csi-bats . && cd -
	@docker run -it \
		-e DOCKER_TAG=$(DOCKER_TAG) \
		-e HELM_REPO=$(HELM_REPO) \
		-v "$(KUBECONFIG):/root/.kube/config" \
		-v "$(PWD)/tests:/code" \
		--network host \
		--entrypoint bash \
		csi-bats
		#bats/test.bats
