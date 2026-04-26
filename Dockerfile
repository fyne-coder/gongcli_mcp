# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/gongctl ./cmd/gongctl \
	&& CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/gongmcp ./cmd/gongmcp

FROM alpine:3.22

ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/arthurlee/gongctl" \
	org.opencontainers.image.description="Unofficial local-first Gong CLI and read-only SQLite-backed MCP adapter" \
	org.opencontainers.image.version="$VERSION"

RUN apk add --no-cache ca-certificates tzdata \
	&& adduser -D -H -u 65532 gongctl \
	&& mkdir -p /data /work \
	&& chown -R 65532:65532 /data /work

COPY --from=build /out/gongctl /usr/local/bin/gongctl
COPY --from=build /out/gongmcp /usr/local/bin/gongmcp

USER 65532:65532
WORKDIR /work

ENTRYPOINT ["/usr/local/bin/gongctl"]
CMD ["--help"]
