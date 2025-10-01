FROM gcr.io/distroless/static-debian12:nonroot 
ARG TARGETPLATFORM
LABEL maintainer="metal-stack authors <info@metal-stack.io>"

WORKDIR /
USER 65532:65532
ENTRYPOINT ["/controller"]
