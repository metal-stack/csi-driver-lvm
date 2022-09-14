GO111MODULE := on
DOCKER_TAG := $(or $(subst _,-,$(GITHUB_TAG_NAME)), latest)


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

.PHONY: tests
tests: | start-test build-provisioner build-plugin do-test clean-test

.PHONY: start-test
start-test:
	@if minikube status >/dev/null 2>/dev/null; then echo "a minikube is already running. Exiting ..."; exit 1; fi
	@echo "Starting minikube testing setup ... please wait ..."
	@./start-minikube-on-linux.sh >/dev/null 2>/dev/null
	@kubectl config view --flatten --minify > tests/files/.kubeconfig
	@minikube docker-env > tests/files/.dockerenv

.PHONY: build-provisioner
build-provisioner:
	@sh -c '. ./tests/files/.dockerenv && docker build -t metalstack/csi-lvmplugin-provisioner:${DOCKER_TAG} . -f cmd/provisioner/Dockerfile'

.PHONY: build-plugin
build-plugin:
	@sh -c '. ./tests/files/.dockerenv && docker build -t metalstack/lvmplugin:${DOCKER_TAG} . '
