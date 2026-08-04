# GitHub Actions 多架构构建专用 Dockerfile

# 构建阶段 1: 前端构建 (Frontend)
# 使用 --platform=$BUILDPLATFORM 强制在构建机本地架构（通常是amd64）运行
# 避免在 arm64 构建时使用 QEMU 模拟，大幅提升速度且产物跨平台通用
FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend-builder
ARG BUILDPLATFORM
WORKDIR /app/web
COPY web/package*.json ./
# 挂载 npm 缓存，加速依赖安装
RUN --mount=type=cache,target=/root/.npm,id=npm-${BUILDPLATFORM},sharing=locked \
    npm ci
COPY web/ .
RUN npm run build:check

# 构建阶段 2: 后端构建 (Backend)
FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine AS backend-builder
ARG BUILDPLATFORM
ARG VERSION=unknown
ARG BUILDTIME=unknown
ARG REVISION=unknown
ARG ENABLE_UPX=0
WORKDIR /app

ENV PATH="/usr/local/go/bin:${PATH}"
# 使用镜像内置的已修复 Go 工具链，避免构建时隐式下载工具链
ENV GOTOOLCHAIN=local
ENV GOWORK=off

# 安装构建依赖
RUN apk add --no-cache git

# 复制 go mod 文件
COPY go.mod go.sum ./
COPY third_party ./third_party

# 挂载 Go 模块缓存，加速依赖下载
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod,id=gomod-${TARGETOS}-${TARGETARCH},sharing=locked \
    go mod download

# 复制源代码
COPY . .

# 复制构建好的前端资源到 internal/web/dist 以便嵌入
COPY --from=frontend-builder /app/web/dist ./internal/web/dist/

# 验证前端资源已复制
RUN ls -la internal/web/dist/ && echo "Frontend assets copied successfully"

# 整理依赖并编译二进制
# 挂载 Go 构建缓存和模块缓存，加速重复构建
RUN --mount=type=cache,target=/root/.cache/go-build,id=gobuild-${TARGETOS}-${TARGETARCH},sharing=locked \
    --mount=type=cache,target=/go/pkg/mod,id=gomod-${TARGETOS}-${TARGETARCH},sharing=locked \
    go mod tidy && \
    BUILD_TIME="${BUILDTIME}" && \
    if [ -z "${BUILD_TIME}" ] || [ "${BUILD_TIME}" = "unknown" ]; then \
      BUILD_TIME="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"; \
    fi && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -tags "with_utls nomsgpack" -ldflags "-s -w -X 'github.com/swordstudiox/vohive-plus/internal/global.Version=${VERSION}' -X 'github.com/swordstudiox/vohive-plus/internal/global.BuildTime=${BUILD_TIME}'" -o vo-hive ./cmd/vohive && \
    if [ "${ENABLE_UPX}" = "1" ] || [ "${ENABLE_UPX}" = "true" ]; then \
      echo "UPX enabled, compressing binary..."; \
      (apk add --no-cache upx >/dev/null 2>&1 || apk add --no-cache upx-ucl >/dev/null 2>&1 || true); \
      if command -v upx >/dev/null 2>&1; then \
        upx --best --lzma /app/vo-hive; \
      else \
        echo "UPX package not available, skip compression."; \
      fi; \
    else \
      echo "UPX disabled."; \
    fi && \
    ls -lh /app/vo-hive

# 运行阶段 (Runtime)
FROM alpine:latest
ARG REVISION=unknown
WORKDIR /app
LABEL org.opencontainers.image.revision=${REVISION}

# 运行时依赖
# - ca-certificates / tzdata: 基础 HTTPS 与时区支持
RUN apk add --no-cache ca-certificates tzdata

# 复制二进制文件
COPY --from=backend-builder /app/vo-hive .

# 创建配置和数据目录
RUN mkdir -p config data logs

# 暴露端口
EXPOSE 7575

# 默认配置路径环境变量
ENV CONFIG_PATH=/app/config/config.yaml
ENV TZ=Asia/Shanghai

# 入口点
ENTRYPOINT ["./vo-hive"]
CMD ["-c", "/app/config/config.yaml"]
