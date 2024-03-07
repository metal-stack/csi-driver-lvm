FROM golang:1.22-alpine as builder
RUN apk add make binutils
COPY / /work
WORKDIR /work
RUN make provisioner

FROM alpine:3.19
LABEL maintainers="Metal Authors"
LABEL description="LVM Driver"

RUN apk add lvm2 lvm2-extra e2fsprogs e2fsprogs-extra smartmontools nvme-cli util-linux device-mapper
COPY --from=builder /work/bin/csi-lvmplugin-provisioner /csi-lvmplugin-provisioner
USER root
ENTRYPOINT ["/csi-lvmplugin-provisioner"]
