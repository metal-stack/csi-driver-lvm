# Copyright 2019 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

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
	docker build -t mwennrich/lvmplugin:latest . 
	docker build -t mwennrich/csi-lvmplugin-provisioner:latest . -f cmd/provisioner/Dockerfile

.PHONY: dockerpush
dockerpush:
	docker push mwennrich/lvmplugin:latest 
	docker push mwennrich/csi-lvmplugin-provisioner:latest 

.PHONY: tests
tests: lvmplugin
	@if minikube status >/dev/null 2>/dev/null; then echo "a minikube is already running. Exiting ..."; exit 1; fi
	@echo "Starting minikube testing setup ... please wait ..."
	@./start-minikube-on-linux.sh >/dev/null 2>/dev/null
	@kubectl config view --flatten --minify > tests/files/.kubeconfig
	@minikube docker-env > tests/files/.dockerenv
	@cp -R helm tests/files
	@sh -c '. ./tests/files/.dockerenv && docker build -t mwennrich/csi-lvmplugin-provisioner:latest . -f cmd/provisioner/Dockerfile'
	@sh -c '. ./tests/files/.dockerenv && docker build -t mwennrich/lvmplugin:latest . '
	@sh -c '. ./tests/files/.dockerenv && docker build -t csi-lvm-tests tests' >/dev/null
	@sh -c '. ./tests/files/.dockerenv && docker run --rm csi-lvm-tests bats /bats'
	@rm tests/files/.dockerenv
	@rm tests/files/.kubeconfig
	@minikube delete

.PHONY: cijob
cijob:
	./tests/files/start-minikube-on-github.sh
	kubectl config view --flatten --minify > tests/files/.kubeconfig
	docker build -t mwennrich/csi-lvmplugin-provisioner:latest . -f cmd/provisioner/Dockerfile
	docker build -t mwennrich/lvmplugin:latest .
	docker build -t csi-lvm-tests tests > /dev/null
	docker run --rm csi-lvm-tests bats /bats

