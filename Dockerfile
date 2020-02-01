FROM alpine:3.11
LABEL maintainers="Metal Authors"
LABEL description="LVM Driver"

# Add util-linux to get a new version of losetup.
RUN apk add util-linux lvm2 e2fsprogs util-linux
COPY ./bin/lvmplugin /lvmplugin
USER root
ENTRYPOINT ["/lvmplugin"]
