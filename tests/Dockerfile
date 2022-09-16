FROM dtzar/helm-kubectl:3.9.4 as helm-kubectl

FROM bats/bats
COPY --from=helm-kubectl /usr/local/bin/helm /usr/local/bin/helm
COPY --from=helm-kubectl /usr/local/bin/kubectl /usr/local/bin/kubectl

ENTRYPOINT [ "bats" ]
