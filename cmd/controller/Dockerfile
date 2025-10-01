FROM gcr.io/distroless/static:nonroot 
ARG TARGETPLATFORM
LABEL maintainer="metal-stack authors <info@metal-stack.io>"

WORKDIR /
COPY bin/${TARGETPLATFORM}/controller /controller
USER root
ENTRYPOINT ["/controller"]
