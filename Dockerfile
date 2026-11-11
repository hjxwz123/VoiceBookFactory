# syntax=docker/dockerfile:1.7
# 声书工坊 VoiceBook Factory — production image.
# Builds the web apps and the Go server on the build machine's own architecture (cross-compiling the
# server), so linux/amd64 and linux/arm64 images build quickly without emulating the toolchains.
#
#   docker build --build-arg VERSION=$(cat VERSION) -t voicebook .
#
# Building in mainland China: point the base images and package sources at domestic mirrors, e.g.
#   --build-arg NODE_IMAGE=<mirror>/library/node:24-slim --build-arg GO_IMAGE=<mirror>/library/golang:1.27
#   --build-arg RUNTIME_IMAGE=<mirror>/library/debian:trixie-slim
#   --build-arg NPM_REGISTRY=https://registry.npmmirror.com --build-arg GOPROXY=https://goproxy.cn,direct
#   --build-arg APT_MIRROR=mirrors.aliyun.com
ARG NODE_IMAGE=node:24-slim
ARG GO_IMAGE=golang:1.27
ARG RUNTIME_IMAGE=debian:trixie-slim

# ---- web (architecture independent) ----
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web
ARG NPM_REGISTRY=
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN if [ -n "$NPM_REGISTRY" ]; then npm config set registry "$NPM_REGISTRY"; fi \
 && npm ci --no-audit --no-fund
COPY VERSION /src/VERSION
COPY web/ ./
RUN npm run build

# ---- server ----
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS server
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=
ARG BUILD_TIME=
ARG GOPROXY=
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    if [ -n "$GOPROXY" ]; then export GOPROXY; fi && go mod download
COPY server/ ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    if [ -n "$GOPROXY" ]; then export GOPROXY; fi && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X voicebook/internal/version.Version=${VERSION} -X voicebook/internal/version.Commit=${COMMIT} -X voicebook/internal/version.BuildTime=${BUILD_TIME}" \
    -o /out/voicebook ./cmd/voicebook

# ---- runtime ----
FROM ${RUNTIME_IMAGE}
ARG VERSION=dev
ARG COMMIT=
ARG APT_MIRROR=
LABEL org.opencontainers.image.title="VoiceBook Factory 声书工坊" \
      org.opencontainers.image.description="AI 有声书工作台：书稿导入、角色分析、画本、配音、审听与交付" \
      org.opencontainers.image.source="https://github.com/hjxwz123/VoiceBookFactory" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
RUN if [ -n "$APT_MIRROR" ]; then \
      sed -i "s|deb.debian.org|$APT_MIRROR|g; s|security.debian.org|$APT_MIRROR|g" /etc/apt/sources.list.d/*.sources /etc/apt/sources.list 2>/dev/null || true; \
    fi \
 && apt-get update \
 && apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --home-dir /data --shell /usr/sbin/nologin voicebook \
 && mkdir -p /data && chown voicebook:voicebook /data
WORKDIR /app
COPY --from=server /out/voicebook /app/voicebook
COPY --from=web /src/web/dist /app/web
ENV VB_ADDR=:8080 VB_DATA_DIR=/data VB_WEB_DIR=/app/web VB_DEV=false TZ=Asia/Shanghai
USER voicebook
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=6s --start-period=40s --retries=5 CMD ["/app/voicebook", "health"]
CMD ["/app/voicebook", "serve"]
