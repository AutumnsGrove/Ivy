# syntax=docker/dockerfile:1

# Ivy's image: one static Go binary with the built frontend embedded. CI builds
# and publishes it; the potato only pulls it, because a cold build there peaks
# near 730 MiB and exhausts swap (ARCHITECTURE.md 9, spike S3).

# --- frontend build -----------------------------------------------------
# Plain JS/CSS/HTML output is architecture-independent, so this stage is pinned
# to the build host's own platform and runs once for every target arch, never
# under QEMU. The frontend build output is never committed; this is the only
# place it comes from.
FROM --platform=$BUILDPLATFORM node:22-bookworm AS frontend-build
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && corepack prepare pnpm@10.32.1 --activate && \
    pnpm install --frozen-lockfile
COPY web/ .
RUN pnpm run build

# --- Go build -----------------------------------------------------------
# Also pinned to BUILDPLATFORM: Go cross-compiles natively with CGO_ENABLED=0,
# so there is no C toolchain and no reason to emulate the target. TARGETOS and
# TARGETARCH are the buildx-provided descriptors of what we compile for.
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# internal/webui/build/ is git-ignored except its placeholder, so the copy
# above brings nothing useful for it; the freshly built frontend lands here.
COPY --from=frontend-build /web/build ./internal/webui/build
# Precompress in place (brotli 11, zstd, gzip 9): the embedded assets are what
# the binary actually serves.
RUN go run ./cmd/ivy-assets -dir internal/webui/build

ARG VERSION=dev-docker
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build,id=go-build-${TARGETOS}-${TARGETARCH} \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w -X main.version=${VERSION}" -o /out/ivy .

# --- runtime ------------------------------------------------------------
# Alpine, not scratch: ca-certificates for the outbound HTTPS (IMAP, SMTP,
# OpenRouter) and tzdata so the local zone at the edge is real. The binary is
# static, so the image is small either way.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S ivy && adduser -S ivy -G ivy
WORKDIR /app
COPY --from=build /out/ivy /app/ivy
# /data holds the config, both databases, the blob store, backups and the
# update signal directory; it is a bind mount, so it survives image swaps.
RUN mkdir -p /data && chown -R ivy:ivy /app /data

# Listen on all interfaces inside the container's network namespace; the host
# decides what to expose. IVY_IN_CONTAINER tells `ivy run` that a host-side
# watcher reads the update signal file.
ENV IVY_LISTEN=0.0.0.0:8418 \
    IVY_DATA_DIR=/data \
    IVY_IN_CONTAINER=1

USER ivy
EXPOSE 8418
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8418/api/v1/health || exit 1

ENTRYPOINT ["/app/ivy"]
CMD ["run", "--config", "/data/ivy.yaml"]
