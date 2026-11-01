#!/bin/sh
# Smoke tests for the install scripts (run in CI): syntax, --help, the Caddyfile handling of
# install-server.sh --domain, and the tailnet logic of both installers, with caddy, systemctl,
# tailscale, curl and hostname stubbed out. Nothing here touches the host.
set -eu
cd "$(dirname "$0")/.."
ROOT=$(pwd)
sh -n install/install-agent.sh
sh -n install/install-server.sh
sh install/install-server.sh --help 2>&1 | grep -q -- '--domain NAME'
sh install/install-server.sh --help 2>&1 | grep -q -- '--tailscale-authkey KEY'
sh install/install-server.sh --help 2>&1 | grep -q -- '--agent-tailnet-only'
sh install/install-agent.sh --help 2>&1 | grep -q -- '--tailscale-hostname NAME'
if command -v shellcheck >/dev/null 2>&1; then
	shellcheck -s sh install/install-agent.sh install/install-server.sh internal/agent/tailnet/install-tailscale.sh
fi

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir "$T/bin" "$T/tsbin"
stub() { # dir name body
	printf '#!/bin/sh\n%s\n' "$3" >"$1/$2"
	chmod +x "$1/$2"
}
for c in caddy systemctl; do stub "$T/bin" "$c" 'exit 0'; done
stub "$T/bin" hostname 'echo Worker_1.Example'

# curl: /agent/v1/bootstrap answers $STUB_BOOTSTRAP with status $STUB_CODE; /agent/v1/ping
# answers {"ok":true} for URLs under $STUB_PING_OK and fails otherwise.
# shellcheck disable=SC2016
stub "$T/bin" curl '
out=/dev/null; w=0; url=""
while [ $# -gt 0 ]; do
	case "$1" in
	-o) out=$2; shift ;;
	-w) w=1; shift ;;
	-H | --data-binary | -m | --retry | --connect-timeout) shift ;;
	http*) url=$1 ;;
	esac
	shift
done
case "$url" in
*/agent/v1/bootstrap*) cat "$STUB_BOOTSTRAP" >"$out"; [ "$w" = 0 ] || printf %s "${STUB_CODE:-200}"; exit 0 ;;
*/agent/v1/ping)
	case "$url" in "${STUB_PING_OK:-none}"*) echo "{\"ok\":true}" >"$out"; exit 0 ;; esac
	exit 7 ;;
esac
exit 22'

# tailscale: state in $TS_STATE (up|down); `up` logs its arguments to $TS_ARGS and fails
# echoing them (key included) when $TS_FAIL=1.
# shellcheck disable=SC2016
stub "$T/tsbin" tailscale '
case "$1" in
status) if [ "$(cat "$TS_STATE")" = up ]; then echo "{\"BackendState\": \"Running\"}"; else echo "{\"BackendState\": \"NeedsLogin\"}"; fi ;;
ip) [ "$(cat "$TS_STATE")" = up ] && echo 100.101.102.103 ;;
up) echo "$*" >>"$TS_ARGS"
	if [ "${TS_FAIL:-0}" = 1 ]; then echo "backend error: invalid key $2"; exit 1; fi
	echo up >"$TS_STATE" ;;
esac
exit 0'

# ---------- install-server.sh: Caddyfile and env helpers ----------
# pull the helper functions out of the installer
# shellcheck disable=SC2016
sed -n '/^log()/,/^need_arg/p;/^CADDYFILE=/,/^TMP=\$(mktemp -d)/p' install/install-server.sh | grep -v '^TMP=' >"$T/f.sh"
cat >>"$T/f.sh" <<'EOF'
set -e
TMP=$(mktemp -d)
# stock Debian Caddyfile is replaced
printf 'example.com {\n\troot * /usr/share/caddy\n\tfile_server\n}\n' >"$CADDYFILE"
setup_caddy a.example.com 127.0.0.1:7300
grep -q '^a.example.com {' "$CADDYFILE"
if grep -q /usr/share/caddy "$CADDYFILE"; then exit 1; fi
if grep -q '@agent' "$CADDYFILE"; then exit 1; fi
# a hand-written Caddyfile is kept and the block appended exactly once
printf 'other.example.com {\n\trespond hi\n}\n' >"$CADDYFILE"
setup_caddy a.example.com 127.0.0.1:7300
setup_caddy a.example.com 127.0.0.1:7300
grep -q '^other.example.com {' "$CADDYFILE"
[ "$(grep -c "^a.example.com {" "$CADDYFILE")" = 1 ]
# tailnet-only: /agent/* answers 404 on the public site, bootstrap stays reachable
rm -f "$CADDYFILE"
setup_caddy a.example.com 127.0.0.1:7300 tailnet-only
grep -q 'path /agent/\*' "$CADDYFILE"
grep -q 'not path /agent/v1/bootstrap' "$CADDYFILE"
grep -q 'respond @agent 404' "$CADDYFILE"
grep -q 'reverse_proxy 127.0.0.1:7300' "$CADDYFILE"
# set_env replaces one key and keeps the rest
ENV_FILE=$TMP/server.env
printf '# c\nATOLL_LISTEN=:7300\nATOLL_LOG_LEVEL=debug\n' >"$ENV_FILE"
set_env ATOLL_AGENT_LISTEN tailscale:7301
set_env ATOLL_LISTEN 127.0.0.1:7300
[ "$(env_get ATOLL_LISTEN)" = 127.0.0.1:7300 ]
[ "$(env_get ATOLL_AGENT_LISTEN)" = tailscale:7301 ]
[ "$(env_get ATOLL_LOG_LEVEL)" = debug ]
[ "$(grep -c '^ATOLL_LISTEN=' "$ENV_FILE")" = 1 ]
rm -rf "$TMP"
EOF
PATH="$T/bin:$PATH" ATOLL_CADDYFILE="$T/Caddyfile" sh "$T/f.sh" 2>/dev/null

# ---------- the Tailscale functions are the same in both installers ----------
shared() { sed -n '/^install_tailscale_apt() {/,/^# ---------- end of the shared Tailscale functions/p' "$1"; }
shared install/install-agent.sh >"$T/shared-agent"
shared install/install-server.sh >"$T/shared-server"
[ -s "$T/shared-agent" ] || {
	echo "shared Tailscale functions not found in install-agent.sh" >&2
	exit 1
}
diff -u "$T/shared-agent" "$T/shared-server" >&2 || {
	echo "install-agent.sh and install-server.sh disagree on the shared Tailscale functions" >&2
	exit 1
}

# ---------- install-agent.sh ----------
KEY=tskey-auth-kTESTSECRET-0123456789
cat >"$T/agent.sh" <<'EOF'
set -eu
ATOLL_INSTALL_SOURCED=1
. "$ROOT/install/install-agent.sh"
TMP=$(mktemp -d)
PING_WAIT=0
SERVER=https://panel.example.com
TOKEN=atl_join_TESTTOKEN
CONFIG=$TMP/agent.json
fail() { echo "FAIL: $*" >&2; exit 1; }
reset_ts() { echo "$1" >"$TS_STATE"; : >"$TS_ARGS"; }
json_ok() { if command -v python3 >/dev/null 2>&1; then python3 -m json.tool "$1" >/dev/null || fail "invalid JSON: $(cat "$1")"; fi; }

# tailnet addresses and machine names
tailscale_is_ip 100.64.0.1 || fail 100.64.0.1
tailscale_is_ip 100.127.255.254 || fail 100.127.255.254
! tailscale_is_ip 100.128.0.1 || fail 100.128.0.1
! tailscale_is_ip 100.63.0.1 || fail 100.63.0.1
! tailscale_is_ip 10.0.0.1 || fail 10.0.0.1
! tailscale_is_ip "" || fail empty
[ "$(tailscale_hostname Worker_1.Example)" = atoll-worker-1-example ] || fail "hostname: $(tailscale_hostname Worker_1.Example)"
[ "$(tailscale_hostname atoll-x)" = atoll-x ] || fail "hostname atoll-x"
[ "$(tailscale_hostname '')" = atoll-node ] || fail "hostname empty"
[ "$(echo "error $KEY here" | redact "$KEY")" = "error [redacted] here" ] || fail redact

# config URLs: already joined through the public URL kept as a fallback
cat >"$CONFIG" <<'JSON'
{
  "server_url": "http://100.64.0.10:7301",
  "fallback_urls": [
    "https://panel.example.com/"
  ],
  "node_id": "srv_aaaaaaaaaaaa",
  "node_token": "atl_node_x"
}
JSON
already_joined || fail "already_joined via fallback"
SERVER=http://100.64.0.10:7301
already_joined || fail "already_joined via server_url"
SERVER=https://other.example.com
! already_joined || fail "already_joined for another server"
SERVER=https://panel.example.com
REJOIN=1
! already_joined || fail "already_joined with --rejoin"
REJOIN=0
add_fallback https://second.example.com
json_ok "$CONFIG"
config_urls | grep -qx https://second.example.com || fail "add_fallback to a list"
config_urls | grep -qx https://panel.example.com || fail "add_fallback lost an entry"

# a fresh config (no fallback_urls) gets one; adding it twice keeps one copy
printf '{\n  "server_url": "http://100.64.0.10:7301",\n  "node_id": "srv_b",\n  "node_token": "atl_node_y"\n}\n' >"$CONFIG"
add_fallback https://panel.example.com
add_fallback https://panel.example.com
json_ok "$CONFIG"
[ "$(config_urls | grep -c '^https://panel.example.com$')" = 1 ] || fail "add_fallback twice"
already_joined || fail "already_joined after add_fallback"
[ "$(stat -c %a "$CONFIG" 2>/dev/null || echo 600)" = 600 ] || fail "config mode"

# bootstrap + tailnet join through the control server's tailnet URL
printf 'ATOLL_NETWORK=tailscale\nATOLL_TS_AUTHKEY=%s\nATOLL_TAILNET=example.ts.net\nATOLL_CONTROL_URL=http://100.64.0.10:7301\nATOLL_PUBLIC_URL=https://panel.example.com\n' "$KEY" >"$STUB_BOOTSTRAP"
reset_ts down
export STUB_PING_OK=http://100.64.0.10:7301
bootstrap 2>"$TMP/log"
[ "$NET_PROVIDER" = tailscale ] && [ "$NET_AUTHKEY" = "$KEY" ] && [ "$NET_CONTROL_URL" = http://100.64.0.10:7301 ] || fail "bootstrap parse"
setup_network 2>>"$TMP/log"
[ "$JOIN_URL" = http://100.64.0.10:7301 ] || fail "JOIN_URL=$JOIN_URL"
[ "$FALLBACK_URL" = https://panel.example.com ] || fail "FALLBACK_URL=$FALLBACK_URL"
for f in --ssh=false --accept-dns=false --accept-routes=false --hostname=atoll-worker-1-example; do
	grep -q -- "$f" "$TS_ARGS" || fail "tailscale up without $f: $(cat "$TS_ARGS")"
done
! grep -q "$KEY" "$TMP/log" || fail "auth key printed"

# already on a tailnet: no new login
reset_ts up
setup_network 2>"$TMP/log"
[ ! -s "$TS_ARGS" ] || fail "tailscale up ran on a running tailnet"
[ "$JOIN_URL" = http://100.64.0.10:7301 ] || fail "JOIN_URL when already up"

# tailnet URL unreachable: public URL, with a warning
reset_ts up
STUB_PING_OK=none
setup_network 2>"$TMP/log"
[ "$JOIN_URL" = "$SERVER" ] && [ -z "$FALLBACK_URL" ] || fail "fallback to public when ping fails"
grep -q 'not reachable over the tailnet' "$TMP/log" || fail "no warning when ping fails"
STUB_PING_OK=http://100.64.0.10:7301

# tailscale up fails: public URL, and the key never shows up in the output
reset_ts down
export TS_FAIL=1
setup_network 2>"$TMP/log"
unset TS_FAIL
[ "$JOIN_URL" = "$SERVER" ] || fail "fallback to public when tailscale up fails"
grep -q '\[redacted\]' "$TMP/log" || fail "tailscale error not shown"
! grep -q "$KEY" "$TMP/log" || fail "auth key printed on failure"

# --no-tailnet and a server without a network provider never touch Tailscale
reset_ts down
NO_TAILNET=1 setup_network 2>/dev/null
NO_TAILNET=0
[ ! -s "$TS_ARGS" ] && [ "$JOIN_URL" = "$SERVER" ] || fail "--no-tailnet"
printf 'ATOLL_NETWORK=\nATOLL_TS_AUTHKEY=\nATOLL_TAILNET=\nATOLL_CONTROL_URL=\nATOLL_PUBLIC_URL=https://panel.example.com\n' >"$STUB_BOOTSTRAP"
bootstrap 2>/dev/null
setup_network 2>/dev/null
[ ! -s "$TS_ARGS" ] && [ "$JOIN_URL" = "$SERVER" ] || fail "network provider none"

# an invalid join token stops before anything is installed; an old server is not fatal
if (export STUB_CODE=401 && bootstrap) 2>/dev/null; then fail "bootstrap accepted a 401"; fi
(export STUB_CODE=404 && bootstrap) 2>/dev/null || fail "bootstrap 404 is fatal"

# the unit is ordered after tailscaled only when Tailscale is installed
write_unit "$TMP/unit"
grep -q '^After=.*tailscaled.service' "$TMP/unit" && grep -q '^Wants=.*tailscaled.service' "$TMP/unit" || fail "unit without tailscaled"
(PATH=$NOTS_PATH && write_unit "$TMP/unit")
! grep -q '^\(After\|Wants\)=.*tailscaled' "$TMP/unit" || fail "unit has tailscaled without Tailscale"
rm -rf "$TMP"
EOF
echo down >"$T/ts.state"
NOTS_PATH="$T/bin:$PATH"
ROOT=$ROOT KEY=$KEY TS_STATE="$T/ts.state" TS_ARGS="$T/ts.args" STUB_BOOTSTRAP="$T/bootstrap.env" \
	NOTS_PATH=$NOTS_PATH PATH="$T/bin:$T/tsbin:$PATH" sh "$T/agent.sh"
echo "installer tests ok"
