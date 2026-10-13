FROM alpine:3.23
ARG TARGETARCH
ARG TARGETVARIANT
LABEL maintainer="metal-stack authors <info@metal-stack.io>"

RUN apk add lvm2 lvm2-extra e2fsprogs e2fsprogs-extra smartmontools nvme-cli util-linux device-mapper xfsprogs xfsprogs-extra cryptsetup
COPY --chmod=755 bin/lvmplugin-linux-${TARGETARCH}${TARGETVARIANT:+-${TARGETVARIANT}} /lvmplugin
USER root
ENTRYPOINT ["/lvmplugin"]
