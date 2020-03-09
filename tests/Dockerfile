FROM alpine

ENV KUBECONFIG /files/.kubeconfig

RUN apk add --update ca-certificates \
 && apk add --update -t deps curl bats openssl \
 && curl -L https://storage.googleapis.com/kubernetes-release/release/$(curl -s https://storage.googleapis.com/kubernetes-release/release/stable.txt)/bin/linux/amd64/kubectl  -o /usr/local/bin/kubectl \
 && chmod +x /usr/local/bin/kubectl \
 && curl -L https://github.com/wercker/stern/releases/download/1.11.0/stern_linux_amd64  -o /usr/local/bin/stern \
 && chmod +x /usr/local/bin/stern \
 && (curl https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3 | bash)

COPY bats /bats
COPY files /files

