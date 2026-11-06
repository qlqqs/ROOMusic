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

FROM alpine:3.23@sha256:f3d55900449227ae9e237ede521e7b665d53a253b42065ba90b9db9275e198ad AS app
RUN apk add --no-cache ca-certificates postgresql18-client \
 && pg_dump --version | grep -q '^pg_dump (PostgreSQL) 18\.' \
 && addgroup -S -g 1000 roomusic && adduser -S -D -H -u 1000 -G roomusic roomusic \
 && mkdir -p /app /data /backups && chown roomusic:roomusic /data /backups && chmod 0700 /backups
COPY --from=build /out/roomusic /app/roomusic
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY --chmod=0755 deploy/production/entrypoint.sh /app/entrypoint.sh
USER 1000:1000
WORKDIR /app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5m --start-interval=5s --retries=3 \
 CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/entrypoint.sh"]
