FROM alpine:3.21
ARG TARGET_ARCH
LABEL maintainer="metal-stack authors <info@metal-stack.io>"

RUN apk add lvm2 lvm2-extra e2fsprogs e2fsprogs-extra smartmontools nvme-cli util-linux device-mapper xfsprogs xfsprogs-extra
COPY bin/${TARGET_ARCH} /lvmplugin
USER root
ENTRYPOINT ["/lvmplugin"]
