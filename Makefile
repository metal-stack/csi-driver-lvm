GO111MODULE := on
DOCKER_TAG := $(or $(subst _,-,$(GITHUB_TAG_NAME)), latest)
TEST_TAG := $(or $(subst .,-,$(subst _,-,$(GITHUB_TAG_NAME))), latest)


all: provisioner lvmplugin

.PHONY: lvmplugin
lvmplugin:
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o ./bin/lvmplugin ./cmd/lvmplugin

.PHONY: provisioner
provisioner:
	go build -tags netgo -o bin/csi-lvmplugin-provisioner cmd/provisioner/*.go
	strip bin/csi-lvmplugin-provisioner

.PHONY: dockerimages
dockerimages:
	docker build -t metalstack/csi-lvmplugin-provisioner:${DOCKER_TAG} . -f cmd/provisioner/Dockerfile
	docker build -t metalstack/lvmplugin:${DOCKER_TAG} .

.PHONY: dockerpush
dockerpush:
	docker push metalstack/lvmplugin:${DOCKER_TAG}
	docker push metalstack/csi-lvmplugin-provisioner:${DOCKER_TAG}

.PHONY: tests
tests: | start-test build-provisioner build-plugin build-test do-test clean-test

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

.PHONY: build-test
build-test:
	@cp -R charts tests/files
	@sh -c '. ./tests/files/.dockerenv && docker build --build-arg docker_tag=${DOCKER_TAG} --build-arg devicepattern="/dev/loop[0-1]" --build-arg pullpolicy=IfNotPresent -t csi-lvm-tests:${DOCKER_TAG} tests' > /dev/null

.PHONY: do-test
do-test:
	@sh -c '. ./tests/files/.dockerenv && docker run --rm csi-lvm-tests:${DOCKER_TAG} bats /bats'

.PHONY: clean-test
clean-test:
	@rm tests/files/.dockerenv
	@rm tests/files/.kubeconfig
	@minikube delete

.PHONY: metalci
metalci: dockerimages dockerpush
	@cp -R charts tests/files
	docker build --build-arg docker_tag=${TEST_TAG} --build-arg devicepattern='/dev/nvme[0-9]n[0-9]' --build-arg pullpolicy=Always -t csi-lvm-tests:${TEST_TAG} tests > /dev/null
	docker run --rm csi-lvm-tests:${TEST_TAG} bats /bats

