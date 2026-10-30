#!/bin/sh
# Copy the built web app (web/dist) into the embed dir, then rebuild atoll-server.
#   (cd web && npm ci && npm run build) && internal/server/webui/sync.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
src="$root/web/dist"
if [ ! -f "$src/index.html" ]; then
  echo "web/dist/index.html not found; run 'npm run build' in web/ first" >&2
  exit 1
fi
rm -rf "$here/dist"
mkdir -p "$here/dist"
cp -R "$src/." "$here/dist/"
echo "copied $(find "$here/dist" -type f | wc -l) file(s) from web/dist"
