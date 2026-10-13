FROM gcr.io/distroless/static:nonroot
ARG TARGETARCH
ARG TARGETVARIANT
LABEL maintainer="metal-stack authors <info@metal-stack.io>"

WORKDIR /
COPY --chmod=755 bin/controller-linux-${TARGETARCH}${TARGETVARIANT:+-${TARGETVARIANT}} /controller
USER 65532:65532
ENTRYPOINT ["/controller"]
