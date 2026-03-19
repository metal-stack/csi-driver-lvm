FROM alpine:3.23
ARG TARGETPLATFORM
LABEL maintainer="metal-stack authors <info@metal-stack.io>"

RUN apk add lvm2 lvm2-extra e2fsprogs e2fsprogs-extra smartmontools nvme-cli util-linux device-mapper xfsprogs xfsprogs-extra drbd-utils
COPY --chmod=755 bin/${TARGETPLATFORM}/lvmplugin /lvmplugin
USER root
ENTRYPOINT ["/lvmplugin"]
