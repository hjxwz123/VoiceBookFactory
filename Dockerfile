# syntax=docker/dockerfile:1.7
# 声书工坊 VoiceBook Factory — production image.
# Builds the web apps and the Go server on the build machine's own architecture (cross-compiling the
# server), so linux/amd64 and linux/arm64 images build quickly without emulating the toolchains.
#
#   docker build --build-arg VERSION=$(cat VERSION) -t voicebook .

# ---- web (architecture independent) ----
FROM --platform=$BUILDPLATFORM node:24-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY VERSION /src/VERSION
COPY web/ ./
RUN npm run build

# ---- server ----
FROM --platform=$BUILDPLATFORM golang:1.27 AS server
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=
ARG BUILD_TIME=
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY server/ ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X voicebook/internal/version.Version=${VERSION} -X voicebook/internal/version.Commit=${COMMIT} -X voicebook/internal/version.BuildTime=${BUILD_TIME}" \
    -o /out/voicebook ./cmd/voicebook

# ---- runtime ----
FROM debian:trixie-slim
ARG VERSION=dev
ARG COMMIT=
LABEL org.opencontainers.image.title="VoiceBook Factory 声书工坊" \
      org.opencontainers.image.description="AI 有声书工作台：书稿导入、角色分析、画本、配音、审听与交付" \
      org.opencontainers.image.source="https://github.com/hjxwz123/VoiceBookFactory" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
RUN apt-get update \
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
