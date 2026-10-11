FROM --platform=$BUILDPLATFORM debian:13 AS build
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git \
    && rm -rf /var/lib/apt/lists/*
ENV MISE_VERSION=2026.10.7 \
    MISE_INSTALL_PATH=/usr/local/bin/mise \
    CGO_ENABLED=0
RUN curl -fsSL https://mise.run | sh
WORKDIR /src
COPY mise.toml mise.lock ./
RUN mise trust && mise install go
COPY go.mod go.sum ./
RUN mise exec go -- go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH mise exec go -- scripts/build.sh binary

FROM gcr.io/distroless/static-debian13:nonroot
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$COMMIT \
      org.opencontainers.image.created=$BUILD_DATE \
      org.opencontainers.image.source="https://github.com/vancanhuit/echo-server"
COPY --from=build /src/bin/echo-server /echo-server
USER 65532:65532
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/echo-server"]
