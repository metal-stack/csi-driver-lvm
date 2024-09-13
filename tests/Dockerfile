FROM dtzar/helm-kubectl:3.14 AS helm-kubectl

FROM bats/bats
COPY --from=helm-kubectl /usr/local/bin/helm /usr/local/bin/helm
COPY --from=helm-kubectl /usr/local/bin/kubectl /usr/local/bin/kubectl

ENTRYPOINT [ "bats" ]
