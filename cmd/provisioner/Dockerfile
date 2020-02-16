FROM golang:1.13-alpine as builder
RUN apk add make binutils
COPY / /work
WORKDIR /work
RUN make provisioner

FROM alpine:3.11
RUN apk add lvm2 e2fsprogs util-linux
COPY --from=builder /work/bin/csi-lvmplugin-provisioner /csi-lvmplugin-provisioner
USER root
ENTRYPOINT ["/csi-lvmplugin-provisioner"]
