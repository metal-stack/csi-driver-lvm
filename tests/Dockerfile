FROM alpine:3.15

ARG docker_tag
ENV DOCKER_TAG=$docker_tag

ARG devicepattern
ENV DEVICEPATTERN=$devicepattern

ARG pullpolicy
ENV PULL_POLICY=$pullpolicy

ENV KUBECONFIG /files/.kubeconfig

RUN apk add --update ca-certificates \
 && apk add --update -t deps curl bats openssl \
 && curl -L https://storage.googleapis.com/kubernetes-release/release/$(curl -s https://storage.googleapis.com/kubernetes-release/release/stable.txt)/bin/linux/amd64/kubectl  -o /usr/local/bin/kubectl \
 && chmod +x /usr/local/bin/kubectl \
 && (curl https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3 | bash)

COPY bats /bats
COPY files /files

