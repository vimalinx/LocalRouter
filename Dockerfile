# syntax=docker/dockerfile:1.7

FROM oven/bun:1.3.14-alpine@sha256:5acc90a93e91ff07bf72aa90a7c9f0fa189765aec90b47bdbf2152d2196383c0 AS web-build
WORKDIR /src/gateway/web-src
COPY gateway/web-src/package.json gateway/web-src/bun.lock ./
RUN bun install --frozen-lockfile
COPY gateway/web-src/ ./
RUN bun run build

FROM golang:1.25.13-alpine@sha256:1e0126852075c9c60731c8ba49088448b91f63e2aed97ca9d1a9791622a05946 AS go-build
ARG VCS_REF=none
ARG BUILD_DATE=unknown
WORKDIR /src
COPY VERSION ./VERSION
COPY gateway/go.mod gateway/go.sum ./gateway/
RUN cd gateway && go mod download
COPY gateway/*.go ./gateway/
COPY gateway/channel-profiles.json ./gateway/channel-profiles.json
COPY gateway/protocols/README.md ./gateway/protocols/README.md
COPY gateway/protocols/schema/ ./gateway/protocols/schema/
COPY gateway/protocols/catalogs/ ./gateway/protocols/catalogs/
COPY --from=web-build /src/gateway/web/ ./gateway/web/
RUN version="$(tr -d '\n' < VERSION)" && \
    CGO_ENABLED=0 go -C gateway build -trimpath \
      -ldflags "-s -w -X main.buildVersion=${version} -X main.buildCommit=${VCS_REF} -X main.buildDate=${BUILD_DATE}" \
      -o /out/localrouter .

FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
ARG VCS_REF=none
ARG BUILD_DATE=unknown
RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 10001 localrouter && \
    adduser -S -D -H -u 10001 -G localrouter localrouter && \
    install -d -m 0700 -o localrouter -g localrouter \
      /var/lib/localrouter/config \
      /var/lib/localrouter/data \
      /var/lib/localrouter/state \
      /var/lib/localrouter/cache
COPY --from=go-build /out/localrouter /usr/local/bin/localrouter
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /usr/share/licenses/localrouter/
LABEL org.opencontainers.image.title="LocalRouter" \
      org.opencontainers.image.description="Local-first AI and API gateway" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.source="https://github.com/vimalinx/LocalRouter" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}"
ENV HOME=/var/lib/localrouter \
    LOCAL_GATEWAY_CONFIG_DIR=/var/lib/localrouter/config \
    LOCAL_GATEWAY_DATA_DIR=/var/lib/localrouter/data \
    LOCAL_GATEWAY_STATE_DIR=/var/lib/localrouter/state \
    LOCAL_GATEWAY_CACHE_DIR=/var/lib/localrouter/cache \
    LOCAL_GATEWAY_HOST=127.0.0.1 \
    LOCAL_GATEWAY_PORT=8317 \
    LOCAL_GATEWAY_UPDATE_CHECK_ENABLED=true \
    LOCAL_GATEWAY_LAN_ENABLED=true \
    LOCAL_GATEWAY_LAN_PORT=8318 \
    GIN_MODE=release
USER 10001:10001
WORKDIR /var/lib/localrouter
EXPOSE 8317 8318
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=6 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${LOCAL_GATEWAY_PORT}/healthz" || exit 1
ENTRYPOINT ["/usr/local/bin/localrouter"]
