FROM golang:1.24-alpine AS builder
RUN apk add make binutils
COPY / /work
WORKDIR /work
RUN make provisioner

FROM alpine:3.21
LABEL maintainers="Metal Authors"
LABEL description="LVM Driver"

RUN apk add lvm2 lvm2-extra e2fsprogs e2fsprogs-extra xfsprogs-extra smartmontools nvme-cli util-linux device-mapper
COPY --from=builder /work/bin/csi-lvmplugin-provisioner /csi-lvmplugin-provisioner
USER root
ENTRYPOINT ["/csi-lvmplugin-provisioner"]
