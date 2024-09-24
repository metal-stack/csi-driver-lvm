FROM golang:1.23-alpine AS builder
RUN apk add make binutils git
WORKDIR /work
COPY cmd cmd
COPY pkg pkg
COPY go.mod go.mod
COPY go.sum go.sum
COPY Makefile Makefile
RUN make lvmplugin

FROM alpine:3.20
LABEL maintainers="Metal Authors"
LABEL description="LVM Driver"

RUN apk add lvm2 lvm2-extra e2fsprogs e2fsprogs-extra smartmontools nvme-cli util-linux device-mapper xfsprogs xfsprogs-extra
COPY --from=builder /work/bin/lvmplugin /lvmplugin
USER root
ENTRYPOINT ["/lvmplugin"]
