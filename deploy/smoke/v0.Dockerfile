# V0 standalone Release Graph exporter；构建上下文固定为已校验归档内的 ROOMusic/。
FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/roomusic-smoke-exporter ./cmd/roomusic-smoke-exporter

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 1000 roomusic \
    && adduser -S -D -H -u 1000 -G roomusic roomusic \
    && mkdir -p /app /music /output \
    && chown -R roomusic:roomusic /app /music /output
COPY --from=builder /out/roomusic-smoke-exporter /app/roomusic-smoke-exporter
USER 1000:1000
WORKDIR /app
ENTRYPOINT ["/app/roomusic-smoke-exporter"]
