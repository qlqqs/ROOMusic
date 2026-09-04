# 仅供真实音乐 Smoke 使用；构建上下文固定为 backend/。
FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/roomusic ./cmd/roomusic

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 1000 roomusic \
    && adduser -S -D -H -u 1000 -G roomusic roomusic \
    && mkdir -p /app /data \
    && chown -R roomusic:roomusic /app /data
COPY --from=builder /out/roomusic /app/roomusic
USER 1000:1000
WORKDIR /app
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=12 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/roomusic"]
