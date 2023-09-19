FROM golang:1.21-alpine as builder
RUN apk add make binutils git
COPY / /work
WORKDIR /work
RUN make lvmplugin

FROM alpine:3.18
LABEL maintainers="Metal Authors"
LABEL description="LVM Driver"

RUN apk add lvm2 lvm2-extra e2fsprogs e2fsprogs-extra smartmontools nvme-cli util-linux device-mapper xfsprogs xfsprogs-extra
COPY --from=builder /work/bin/lvmplugin /lvmplugin
USER root
ENTRYPOINT ["/lvmplugin"]
