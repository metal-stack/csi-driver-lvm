FROM alpine:3.11
LABEL maintainers="Metal Authors"
LABEL description="LVM Driver"

# Add util-linux to get a new version of losetup.
RUN apk add lvm2 lvm2-extra e2fsprogs e2fsprogs-extra smartmontools nvme-cli util-linux device-mapper
COPY ./bin/lvmplugin /lvmplugin
USER root
ENTRYPOINT ["/lvmplugin"]
