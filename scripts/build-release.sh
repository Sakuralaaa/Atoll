#!/bin/sh
# Build release binaries for linux/amd64 and linux/arm64 into dist/.
#   VERSION=v0.1.0 scripts/build-release.sh
# Expects internal/server/webui/dist to hold the built panel (run web build + sync.sh first).
set -eu
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
MOD=github.com/Sakuralaaa/Atoll
# The source revision, stamped explicitly. Go's own vcs stamp would say "-dirty" on every release:
# sync.sh replaces the tracked placeholder in internal/server/webui/dist with the built panel. That
# directory is a build output, so it is left out of the dirty check; anything else modified is real.
COMMIT=${COMMIT:-$(git rev-parse --short=12 HEAD 2>/dev/null || echo "")}
if [ -n "$COMMIT" ] && [ -n "$(git status --porcelain --untracked-files=no -- . ':(exclude)internal/server/webui/dist' 2>/dev/null)" ]; then
  COMMIT="$COMMIT-dirty"
fi
OUT=${OUT:-dist}
rm -rf "$OUT" && mkdir -p "$OUT"
for arch in amd64 arm64; do
  for cmd in atoll atoll-agent atoll-server; do
    ld="-s -w -X main.version=$VERSION -X $MOD/internal/agent.Version=$VERSION -X $MOD/internal/server.Version=$VERSION -X $MOD/internal/server.Commit=$COMMIT"
    CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "$ld" -o "$OUT/$cmd-linux-$arch" "./cmd/$cmd"
  done
done
# CLI for macOS and Windows too (deploying from a laptop)
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/atoll-darwin-$arch" ./cmd/atoll
  CGO_ENABLED=0 GOOS=windows GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/atoll-windows-$arch.exe" ./cmd/atoll
done
cp install/install-agent.sh install/install-server.sh "$OUT/"
(cd "$OUT" && sha256sum * > SHA256SUMS)
ls -la "$OUT"
