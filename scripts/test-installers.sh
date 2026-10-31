#!/bin/sh
# Smoke tests for the install scripts (run in CI): syntax, --help, and the Caddyfile
# handling of install-server.sh --domain with caddy/systemctl stubbed out.
set -eu
cd "$(dirname "$0")/.."
sh -n install/install-agent.sh
sh -n install/install-server.sh
sh install/install-server.sh --help 2>&1 | grep -q -- '--domain NAME'

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir "$T/bin"
for c in caddy systemctl; do printf '#!/bin/sh\nexit 0\n' >"$T/bin/$c"; chmod +x "$T/bin/$c"; done
# pull the helper functions out of the installer
sed -n '/^log()/,/^need_arg/p;/^CADDYFILE=/,/^TMP=\$(mktemp -d)/p' install/install-server.sh | grep -v '^TMP=' >"$T/f.sh"
cat >>"$T/f.sh" <<'EOF'
set -e
TMP=$(mktemp -d)
# stock Debian Caddyfile is replaced
printf 'example.com {\n\troot * /usr/share/caddy\n\tfile_server\n}\n' >"$CADDYFILE"
setup_caddy a.example.com 127.0.0.1:7300
grep -q '^a.example.com {' "$CADDYFILE"
if grep -q /usr/share/caddy "$CADDYFILE"; then exit 1; fi
# a hand-written Caddyfile is kept and the block appended exactly once
printf 'other.example.com {\n\trespond hi\n}\n' >"$CADDYFILE"
setup_caddy a.example.com 127.0.0.1:7300
setup_caddy a.example.com 127.0.0.1:7300
grep -q '^other.example.com {' "$CADDYFILE"
[ "$(grep -c "^a.example.com {" "$CADDYFILE")" = 1 ]
EOF
PATH="$T/bin:$PATH" ATOLL_CADDYFILE="$T/Caddyfile" sh "$T/f.sh" 2>/dev/null
echo "installer tests ok"
