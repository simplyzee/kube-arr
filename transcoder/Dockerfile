# kube-arr-transcoder: a static binary, copied into the media server by an
# init container. Built for every platform the LinuxServer.io images support.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/kube-arr-transcoder .

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/simplyzee/kube-arr" \
      org.opencontainers.image.description="Dispatches Plex, Jellyfin and Emby transcodes to Kubernetes pods" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/kube-arr-transcoder /kube-arr-transcoder
ENTRYPOINT ["/kube-arr-transcoder"]
