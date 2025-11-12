FROM dtzar/helm-kubectl:3.16 AS helm-kubectl

FROM registry.k8s.io/kustomize/kustomize:v5.0.0 AS kustomize

RUN which kustomize

FROM bats/bats
RUN apk --update add jq
COPY --from=helm-kubectl /usr/local/bin/helm /usr/local/bin/helm
COPY --from=helm-kubectl /usr/local/bin/kubectl /usr/local/bin/kubectl
COPY --from=kustomize /app/kustomize /usr/local/bin/kustomize

ENTRYPOINT [ "bats" ]
