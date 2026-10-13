FROM gcr.io/distroless/static:nonroot
ARG TARGETARCH
LABEL maintainer="metal-stack authors <info@metal-stack.io>"

WORKDIR /
COPY --chmod=755 controller-linux-${TARGETARCH} /controller
USER 65532:65532
ENTRYPOINT ["/controller"]
