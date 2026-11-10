# syntax=docker/dockerfile:1
# Atoll control server (atoll-server) image.
#
#   docker build -f deploy/docker/Dockerfile -t atoll-server .        (from the repository root)
#
# The panel is built with Node and embedded; atoll-server is built for the target platform and
# atoll-agent for linux/amd64 and linux/arm64. The agent binaries ship in the image because new
# nodes and agent upgrades download them from this server (<data>/downloads), and they must be the
# same version as atoll-server. The entrypoint copies them into the data volume on every start.
#
# Builder stages run on the build platform and cross-compile (CGO off), so no emulation is needed.

ARG GO_VERSION=1.26.10
ARG NODE_VERSION=22
ARG ALPINE_VERSION=3.22

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
# golang images pin GOTOOLCHAIN=local; let go.mod's toolchain line win if the image is older.
ENV GOTOOLCHAIN=auto CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/ /src/web/dist/
RUN sh internal/server/webui/sync.sh
ARG VERSION=dev
ARG COMMIT=
ARG TARGETOS=linux
ARG TARGETARCH
RUN set -eu; \
    mod=github.com/Sakuralaaa/Atoll; \
    ld="-s -w -X main.version=$VERSION -X $mod/internal/agent.Version=$VERSION -X $mod/internal/server.Version=$VERSION -X $mod/internal/server.Commit=$COMMIT"; \
    mkdir -p /out/agent; \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "$ld" -o /out/atoll-server ./cmd/atoll-server; \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "$ld" -o /out/atoll ./cmd/atoll; \
    for a in amd64 arm64; do \
      GOOS=linux GOARCH=$a go build -trimpath -ldflags "$ld" -o /out/agent/atoll-agent-linux-$a ./cmd/atoll-agent; \
    done; \
    (cd /out/agent && sha256sum atoll-agent-linux-* > SHA256SUMS)

FROM alpine:${ALPINE_VERSION}
ARG VERSION=dev
ARG COMMIT=
LABEL org.opencontainers.image.title="atoll-server" \
      org.opencontainers.image.description="Atoll control server: REST API, web panel and agent hub" \
      org.opencontainers.image.source="https://github.com/Sakuralaaa/Atoll" \
      org.opencontainers.image.documentation="https://sakuralaaa.github.io/Atoll/install-docker" \
      org.opencontainers.image.licenses="AGPL-3.0" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$COMMIT"
# ca-certificates: GitHub, Cloudflare and S3 over HTTPS; tzdata: local times in logs and schedules.
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 atoll \
 && adduser -S -D -H -u 10001 -G atoll -h /var/lib/atoll -s /sbin/nologin atoll \
 && install -d -o 10001 -g 10001 -m 0700 /var/lib/atoll
COPY --from=build /out/atoll-server /out/atoll /usr/local/bin/
COPY --from=build /out/agent/ /opt/atoll/agent/
COPY deploy/docker/entrypoint.sh /usr/local/bin/atoll-entrypoint
ENV ATOLL_DEPLOYMENT=docker \
    ATOLL_LISTEN=:7300 \
    ATOLL_DATA=/var/lib/atoll
USER 10001:10001
WORKDIR /var/lib/atoll
VOLUME ["/var/lib/atoll"]
EXPOSE 7300
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${ATOLL_LISTEN##*:}/healthz" || exit 1
ENTRYPOINT ["/usr/local/bin/atoll-entrypoint"]
CMD ["serve"]
