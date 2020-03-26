GO111MODULE := on
DOCKER_TAG := $(or ${GITHUB_TAG_NAME}, latest)

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
	docker build -t mwennrich/csi-lvmplugin-provisioner:${DOCKER_TAG} . -f cmd/provisioner/Dockerfile
	docker build -t mwennrich/lvmplugin:${DOCKER_TAG} .

.PHONY: dockerpush
dockerpush:
	docker push mwennrich/lvmplugin:${DOCKER_TAG}
	docker push mwennrich/csi-lvmplugin-provisioner:${DOCKER_TAG}

.PHONY: tests
tests:
	@if minikube status >/dev/null 2>/dev/null; then echo "a minikube is already running. Exiting ..."; exit 1; fi
	@echo "Starting minikube testing setup ... please wait ..."
	@./start-minikube-on-linux.sh >/dev/null 2>/dev/null
	@kubectl config view --flatten --minify > tests/files/.kubeconfig
	@minikube docker-env > tests/files/.dockerenv
	@cp -R helm tests/files
	@sh -c '. ./tests/files/.dockerenv && docker build -t mwennrich/csi-lvmplugin-provisioner:latest . -f cmd/provisioner/Dockerfile'
	@sh -c '. ./tests/files/.dockerenv && docker build -t mwennrich/lvmplugin:latest . '
	@sh -c '. ./tests/files/.dockerenv && docker build --build-arg docker_tag=${DOCKER_TAG} --build-arg devicepattern="/dev/loop[0-1]" -t csi-lvm-tests tests' > /dev/null
	@sh -c '. ./tests/files/.dockerenv && docker run --rm csi-lvm-tests bats /bats'
	@rm tests/files/.dockerenv
	@rm tests/files/.kubeconfig
	@minikube delete

.PHONY: metalci
metalci: dockerimages dockerpush
	@cp -R helm tests/files
	docker build --build-arg docker_tag=${DOCKER_TAG} --build-arg devicepattern='/dev/nvme[0-9]n[0-9]' -t csi-lvm-tests tests > /dev/null
	docker run --rm csi-lvm-tests bats /bats

