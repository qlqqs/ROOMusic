# syntax=docker/dockerfile:1
# 上下文是仓库根；Node 在构建平台生成前端，Go 校验并内嵌产物，运行镜像无需 Node。
FROM --platform=$BUILDPLATFORM node:24.22.1-alpine3.23@sha256:0468540aa58203ff9e40c2aab3907c52644ab818af6176b1c70a026137dc92a2 AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
COPY backend/internal/app/testdata/api-samples/ /src/backend/internal/app/testdata/api-samples/
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.25.10-alpine3.22@sha256:26b4d7113039cd51356bd7930ecafd1031d2975dc3b6940ec8ed09457e17cf95 AS build
ARG TARGETOS
ARG TARGETARCH
ARG ROOMUSIC_VERSION=""
ARG ROOMUSIC_COMMIT=""
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /src/backend/cmd/roomusic/web/dist cmd/roomusic/web/dist
# 可选的 AcoustID 应用密钥（发布流水线传入，去掉空白后只许字母数字）；secret 内容不参与构建缓存键，本地先不带 secret 构建、再带 secret 时需使用 --no-cache。
RUN --mount=type=secret,id=acoustid_client \
    acoustid_ldflags= \
 && if [ -s /run/secrets/acoustid_client ]; then \
        key=$(tr -d '[:space:]' < /run/secrets/acoustid_client); \
        case "$key" in *[!A-Za-z0-9]*) echo 'AcoustID 应用密钥只能包含字母和数字' >&2; exit 1;; esac; \
        acoustid_ldflags=" -X github.com/qlqq/roomusic/backend/internal/sources/acoustid.bundledClientKey=${key}"; \
    fi \
 && test -f cmd/roomusic/web/dist/index.html \
 && go test ./cmd/roomusic/ -run TestEmbeddedAssetsAreContentHashed \
 && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w -X github.com/qlqq/roomusic/backend/internal/platform/buildinfo.version=${ROOMUSIC_VERSION} -X github.com/qlqq/roomusic/backend/internal/platform/buildinfo.commit=${ROOMUSIC_COMMIT}${acoustid_ldflags}" \
    -o /out/roomusic ./cmd/roomusic

FROM mwader/static-ffmpeg@sha256:11a44711684c0b9f754c047dcd64235b8b52deab251bd0e0a86f22faa160749c AS ffmpeg

# 上游 x86_64 包是静态构建；arm64 包链接 glibc 2.28 以上（libc、libm、libstdc++、libgcc，FFmpeg 已静态链入），
# gcompat 缺 fcntl64 等符号，跑不起来。arm64 上带一份 Debian 的 glibc 运行库放进 /opt/fpcalc/lib，
# 由包装脚本经它的加载器启动 fpcalc，不进入 Alpine 的系统库路径。
FROM debian:12-slim@sha256:e67eeead736ff7bbe258ef088bff683635c561b1fcfefad0806c1df4ab49ce0e AS glibc
ARG TARGETARCH
RUN mkdir -p /out/lib \
 && if [ "$TARGETARCH" = arm64 ]; then \
      for f in ld-linux-aarch64.so.1 libc.so.6 libm.so.6 libstdc++.so.6 libgcc_s.so.1; do \
        cp -L "/usr/lib/aarch64-linux-gnu/$f" /out/lib/; \
      done; \
    fi

FROM alpine:3.23@sha256:f3d55900449227ae9e237ede521e7b665d53a253b42065ba90b9db9275e198ad AS fpcalc
ARG TARGETARCH
ADD --checksum=sha256:c968ad4a0821592a79b158fb2e1f46cbe0a22a0a1dbc0743c67d68e75d15d2bb https://github.com/acoustid/chromaprint/releases/download/v1.6.2/chromaprint-fpcalc-1.6.2-linux-x86_64.tar.gz /amd64.tar.gz
ADD --checksum=sha256:3aa1498126ed272d805bc9d081c77965c9e1e1a0891bb112c13b56ef2ad47bad https://github.com/acoustid/chromaprint/releases/download/v1.6.2/chromaprint-fpcalc-1.6.2-linux-arm64.tar.gz /arm64.tar.gz
COPY --from=glibc /out/lib /glibc
RUN mkdir -p /out/usr/local/bin \
 && if [ "$TARGETARCH" = arm64 ]; then \
      mkdir -p /out/opt/fpcalc \
      && tar -xzf /arm64.tar.gz -C /out/opt/fpcalc --strip-components=1 \
      && cp -a /glibc /out/opt/fpcalc/lib \
      && printf '#!/bin/sh\nexec /opt/fpcalc/lib/ld-linux-aarch64.so.1 --library-path /opt/fpcalc/lib /opt/fpcalc/fpcalc "$@"\n' > /out/usr/local/bin/fpcalc \
      && chmod 0755 /out/usr/local/bin/fpcalc \
      && cp -a /out/opt/fpcalc /opt/fpcalc; \
    else \
      tar -xzf /amd64.tar.gz -C /out/usr/local/bin --strip-components=1; \
    fi \
 && /out/usr/local/bin/fpcalc -version | grep -q '^fpcalc version 1\.6\.2 ('

FROM alpine:3.23@sha256:f3d55900449227ae9e237ede521e7b665d53a253b42065ba90b9db9275e198ad AS app
RUN apk add --no-cache ca-certificates postgresql18-client \
 && pg_dump --version | grep -q '^pg_dump (PostgreSQL) 18\.' \
 && addgroup -S -g 1000 roomusic && adduser -S -D -H -u 1000 -G roomusic roomusic \
 && mkdir -p /app /data /backups && chown roomusic:roomusic /data /backups && chmod 0700 /backups
COPY --from=build /out/roomusic /app/roomusic
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY --from=fpcalc /out/ /
# 在运行镜像里按应用的调用方式（s16le 管道）算一次合成正弦波的指纹。
RUN /usr/local/bin/fpcalc -version | grep -q '^fpcalc version 1\.6\.2 (' \
 && /usr/local/bin/ffmpeg -v error -f lavfi -i sine=frequency=440:duration=15 -f s16le -ac 1 -ar 11025 - \
  | /usr/local/bin/fpcalc -format s16le -rate 11025 -channels 1 -length 120 -json - | grep -q '"fingerprint": *"[^"]'
COPY --chmod=0755 deploy/production/entrypoint.sh /app/entrypoint.sh
USER 1000:1000
WORKDIR /app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5m --start-interval=5s --retries=3 \
 CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/entrypoint.sh"]
