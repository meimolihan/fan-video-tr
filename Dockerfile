# syntax=docker/dockerfile:1
#
# fan-video-tr 多阶段构建：前端通过 go:embed 打进二进制，运行时镜像只带二进制 + FFmpeg。
# 构建参数：
#   FVT_VERSION  版本号（写入 internal/version.Version）
#   GOPROXY      Go 模块代理（国内可换 goproxy.cn）
#   TAGS         构建标签
#   TARGETOS / TARGETARCH 由 buildx 注入
ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.20
ARG FVT_VERSION=dev
ARG GOPROXY=https://proxy.golang.org,direct
ARG TAGS=""

# ---------- 构建阶段 ----------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG FVT_VERSION
ARG GOPROXY
ARG TAGS

ENV CGO_ENABLED=0 \
    GOPROXY=${GOPROXY} \
    GOFLAGS=-trimpath

WORKDIR /src

# 先拷贝依赖清单，命中缓存时不必重新下载依赖
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# ldflags 注入版本号；-tags 预留给后续构建变体
RUN go build -tags "${TAGS}" \
      -ldflags "-s -w -X github.com/meimolihan/fan-video-tr/internal/version.Version=${FVT_VERSION}" \
      -o /out/fan-video-tr .

# ---------- 运行阶段 ----------
FROM alpine:${ALPINE_VERSION}

ARG FVT_VERSION
LABEL org.opencontainers.image.title="fan-video-tr" \
      org.opencontainers.image.description="内置 FFmpeg 的视频转码 Web 服务（原生前端已内嵌）" \
      org.opencontainers.image.version="${FVT_VERSION}" \
      org.opencontainers.image.source="https://github.com/meimolihan/fan-video-tr"

ENV TZ=Asia/Shanghai \
    FVT_APP_PORT=8790 \
    FVT_APP_DATA_DIR=/data \
    FVT_APP_OUTPUT_DIR=output \
    FVT_FFMPEG_ACCEL=auto

# ffmpeg（含 libx264/x265/libvpx/svt-av1/opus）与 su-exec（降权运行）
# 中文字体用于缩略图/日志无实际依赖，但保留 dejavu 避免缺字体告警
RUN set -eux; \
    # ffmpeg 在 community 仓库，个别基础镜像默认注释掉了该行
    if grep -q '^#.*/community' /etc/apk/repositories; then \
        sed -i 's|^#\(/etc/apk/repositories.*/community\)|\1|' /etc/apk/repositories; \
    fi; \
    apk add --no-cache ffmpeg su-exec tzdata ca-certificates; \
    addgroup -g 1000 fvt; \
    adduser -D -u 1000 -G fvt -h /data fvt; \
    mkdir -p /data; \
    chown -R fvt:fvt /data

COPY --from=builder /out/fan-video-tr /usr/local/bin/fan-video-tr
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME ["/data"]
EXPOSE 8790

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${FVT_APP_PORT}/api/health" >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/entrypoint.sh"]
