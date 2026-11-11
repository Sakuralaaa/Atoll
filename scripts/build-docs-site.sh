#!/bin/sh
# Build the documentation site (VitePress, docs/) with the online demo of the panel in /demo/.
#   scripts/build-docs-site.sh            # output: docs/.vitepress/dist (served under /Atoll/)
#   SKIP_INSTALL=1 scripts/build-docs-site.sh   # reuse existing node_modules
# The demo is the panel built with VITE_DEMO=1: all data is in-memory example data, no backend.
set -eu
cd "$(dirname "$0")/.."
BASE=${DOCS_BASE:-/Atoll/}
if [ -z "${SKIP_INSTALL:-}" ]; then
	(cd docs && npm ci --no-audit --no-fund)
	(cd web && npm ci --no-audit --no-fund)
fi
(cd docs && npx vitepress build)
(cd web && VITE_DEMO=1 npx vite build --base="${BASE}demo/" --outDir ../docs/.vitepress/dist/demo --emptyOutDir)
# GitHub Pages: serve files and folders starting with "_" as they are
touch docs/.vitepress/dist/.nojekyll
echo "site: docs/.vitepress/dist (demo: ${BASE}demo/?demo=1)"
