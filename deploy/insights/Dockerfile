FROM --platform=$BUILDPLATFORM golang:1.25.10-alpine3.22@sha256:26b4d7113039cd51356bd7930ecafd1031d2975dc3b6940ec8ed09457e17cf95 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY backend/go.mod ./
COPY backend/cmd/roomusic-insights ./cmd/roomusic-insights
COPY backend/internal/insights/collector ./internal/insights/collector
COPY backend/internal/insights/schema ./internal/insights/schema
COPY backend/internal/platform/uuid ./internal/platform/uuid
COPY backend/internal/platform/buildinfo ./internal/platform/buildinfo
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/roomusic-insights ./cmd/roomusic-insights

FROM alpine:3.23@sha256:f3d55900449227ae9e237ede521e7b665d53a253b42065ba90b9db9275e198ad
COPY --from=build /out/roomusic-insights /usr/local/bin/roomusic-insights
COPY LICENSE /usr/share/doc/roomusic-insights/LICENSE
RUN mkdir /data && chown 1000:1000 /data
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/roomusic-insights"]
CMD ["-data", "/data"]
