# syntax=docker/dockerfile:1

# ============ 构建阶段：Go 交叉编译（利用 BUILDPLATFORM，arm64 无需 QEMU 模拟）============
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src

# 先复制依赖清单，最大化利用构建缓存（本项目零第三方依赖）
COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/cfnb ./cmd/cfnb

# ============ 运行阶段：极简 Alpine（仅 ca-certificates + tzdata + 静态二进制）============
FROM alpine:3.20

# ca-certificates 用于 HTTPS 请求，tzdata 用于 TZ 环境变量
RUN apk add --no-cache ca-certificates tzdata

ENV TZ=Asia/Shanghai \
    RUN_INTERVAL=0

WORKDIR /app

# 二进制与配置必须同目录：程序通过 os.Executable() 定位 config.json / ip.txt
COPY --from=builder /out/cfnb /app/cfnb
COPY deploy/app/config.json /app/config.json
COPY deploy/docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/cfnb /app/docker-entrypoint.sh

# RUN_INTERVAL: 循环间隔秒数；0 或未设置 = 只运行一次
ENTRYPOINT ["/app/docker-entrypoint.sh"]
