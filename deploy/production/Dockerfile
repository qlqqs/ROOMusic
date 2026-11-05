# syntax=docker/dockerfile:1
# 上下文必须是 Git 导出树；前端使用已提交的内嵌产物，不安装 Node。
FROM golang:1.25.10-alpine3.22@sha256:26b4d7113039cd51356bd7930ecafd1031d2975dc3b6940ec8ed09457e17cf95 AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN test -f cmd/roomusic/web/index.html \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/roomusic ./cmd/roomusic

FROM mwader/static-ffmpeg@sha256:11a44711684c0b9f754c047dcd64235b8b52deab251bd0e0a86f22faa160749c AS ffmpeg

# 与 compose.yaml 的 x-postgres-image 保持完全一致。
FROM postgres:18.1-alpine3.23@sha256:aa6eb304ddb6dd26df23d05db4e5cb05af8951cda3e0dc57731b771e0ef4ab29 AS tools
COPY --chmod=0755 deploy/production/backup.sh deploy/production/restore.sh /opt/roomusic/

FROM alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f AS app
RUN apk add --no-cache ca-certificates postgresql17-client \
 && addgroup -S -g 1000 roomusic && adduser -S -D -H -u 1000 -G roomusic roomusic \
 && mkdir -p /app /data && chown roomusic:roomusic /data
COPY --from=build /out/roomusic /app/roomusic
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY --chmod=0755 deploy/production/entrypoint.sh /app/entrypoint.sh
USER 1000:1000
WORKDIR /app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
 CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/entrypoint.sh"]
