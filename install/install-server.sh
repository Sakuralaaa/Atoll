#!/bin/sh
# Atoll control server installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-server.sh \
#     | sudo sh -s -- [--domain atoll.example.com] [--public-url URL] [--listen :7300] [--version v0.1.0]
#
# What it does (safe to re-run; upgrades in place):
#   1. detects the architecture (amd64 / arm64)
#   2. downloads atoll-server (and atoll-agent binaries to serve to new nodes) from GitHub releases
#   3. writes /etc/atoll/server.env and the atoll-server systemd unit (data dir /var/lib/atoll)
#   4. (re)starts the service and waits for /healthz
#   5. with --domain: installs Caddy and serves the panel on https://DOMAIN (automatic TLS)
#
# Options:
#   --domain NAME      serve the panel on https://NAME through Caddy with an automatic
#                      Let's Encrypt certificate (DNS for NAME must point here; ports 80/443
#                      free). Implies --listen 127.0.0.1:7300 and --public-url https://NAME
#   --public-url URL   URL agents and the CLI use to reach this server (ATOLL_PUBLIC_URL)
#   --listen ADDR      listen address (default :7300)
#   --version TAG      release tag to install (default: latest)
#   --binary PATH      install this local atoll-server binary instead of downloading
#   --no-agent-binaries  do not mirror atoll-agent binaries into /var/lib/atoll/downloads
set -eu

PUBLIC_URL=""
DOMAIN=""
LISTEN=":7300"
LISTEN_SET=0
VERSION="latest"
LOCAL_BINARY=""
AGENT_BINARIES=1

BIN=${ATOLL_SERVER_BIN:-/usr/local/bin/atoll-server}
ENV_FILE=${ATOLL_SERVER_ENV:-/etc/atoll/server.env}
UNIT=${ATOLL_SERVER_UNIT:-/etc/systemd/system/atoll-server.service}
DATA=${ATOLL_DATA:-/var/lib/atoll}
REPO=https://github.com/Sakuralaaa/Atoll

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die() {
	printf '\033[1;31merror:\033[0m %s\n' "$*" >&2
	exit 1
}

usage() {
	sed -n '2,/^set -eu/p' "$0" 2>/dev/null | grep '^#' | sed 's/^# \{0,1\}//' >&2 || true
	exit 2
}

need_arg() { [ $# -ge 2 ] && [ -n "$2" ] || die "$1 needs a value"; }

while [ $# -gt 0 ]; do
	case "$1" in
	--public-url) need_arg "$@"; PUBLIC_URL=$2; shift 2 ;;
	--public-url=*) PUBLIC_URL=${1#*=}; shift ;;
	--domain) need_arg "$@"; DOMAIN=$2; shift 2 ;;
	--domain=*) DOMAIN=${1#*=}; shift ;;
	--listen) need_arg "$@"; LISTEN=$2; LISTEN_SET=1; shift 2 ;;
	--listen=*) LISTEN=${1#*=}; LISTEN_SET=1; shift ;;
	--version) need_arg "$@"; VERSION=$2; shift 2 ;;
	--version=*) VERSION=${1#*=}; shift ;;
	--binary) need_arg "$@"; LOCAL_BINARY=$2; shift 2 ;;
	--binary=*) LOCAL_BINARY=${1#*=}; shift ;;
	--no-agent-binaries) AGENT_BINARIES=0; shift ;;
	-h | --help) usage ;;
	*) die "unknown option: $1" ;;
	esac
done

if [ -n "$DOMAIN" ]; then
	DOMAIN=$(printf '%s' "$DOMAIN" | sed -e 's#^[a-zA-Z]*://##' -e 's#/.*$##')
	case "$DOMAIN" in *[!A-Za-z0-9.-]* | "" | .* | *.) die "--domain needs a host name like atoll.example.com" ;; esac
	[ -n "$PUBLIC_URL" ] || PUBLIC_URL="https://$DOMAIN"
	[ "$LISTEN_SET" = 1 ] || LISTEN="127.0.0.1:7300"
	LISTEN_SET=1
fi

[ "$(id -u)" = 0 ] || die "run as root (sudo)"
[ "$(uname -s)" = Linux ] || die "atoll-server supports Linux only"
command -v systemctl >/dev/null 2>&1 || die "systemd is required"

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) die "unsupported architecture $(uname -m) (amd64 and arm64 are supported)" ;;
esac

if [ "$VERSION" = latest ]; then
	RELEASE="$REPO/releases/latest/download"
else
	RELEASE="$REPO/releases/download/$VERSION"
fi

fetch() { # url dest
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "curl or wget is required"
	fi
}

CADDYFILE=${ATOLL_CADDYFILE:-/etc/caddy/Caddyfile}

# setup_caddy DOMAIN UPSTREAM: install Caddy (Debian/Ubuntu package, else Caddy's apt repo)
# and add a site block for DOMAIN. An existing Caddyfile that is not the stock one is kept;
# the block is appended once.
setup_caddy() {
	if ! command -v caddy >/dev/null 2>&1; then
		command -v apt-get >/dev/null 2>&1 || die "--domain installs Caddy with apt; install Caddy yourself on this distribution and proxy $1 to $2"
		log "installing Caddy"
		export DEBIAN_FRONTEND=noninteractive
		apt-get update -qq >/dev/null 2>&1 || true
		if ! apt-get install -y -qq caddy >/dev/null 2>&1; then
			apt-get install -y -qq --no-install-recommends debian-keyring debian-archive-keyring apt-transport-https gnupg curl >/dev/null 2>&1 || true
			install -d -m 0755 /usr/share/keyrings
			fetch https://dl.cloudsmith.io/public/caddy/stable/gpg.key "$TMP/caddy.key" || die "could not download Caddy's signing key"
			gpg --dearmor <"$TMP/caddy.key" >/usr/share/keyrings/caddy-stable-archive-keyring.gpg || die "gpg is required to add Caddy's apt repository"
			fetch https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt /etc/apt/sources.list.d/caddy-stable.list || die "could not add Caddy's apt repository"
			apt-get update -qq >/dev/null 2>&1 || true
			apt-get install -y -qq caddy >/dev/null || die "Caddy installation failed"
		fi
	fi
	install -d -m 0755 "$(dirname "$CADDYFILE")"
	block="# atoll panel (install-server.sh --domain)
$1 {
	reverse_proxy $2
}"
	if [ ! -s "$CADDYFILE" ] || grep -q '^# atoll-managed' "$CADDYFILE" || grep -q '/usr/share/caddy' "$CADDYFILE"; then
		printf '# atoll-managed: rewritten by install-server.sh --domain\n%s\n' "$block" >"$CADDYFILE"
	elif grep -Eq "^$1([ ,]|\$)" "$CADDYFILE"; then
		log "$CADDYFILE already has a site block for $1; leaving it as is"
	else
		printf '\n%s\n' "$block" >>"$CADDYFILE"
	fi
	systemctl enable caddy >/dev/null 2>&1 || true
	if systemctl is-active --quiet caddy; then
		systemctl reload caddy || systemctl restart caddy || die "Caddy did not accept $CADDYFILE; see journalctl -u caddy"
	else
		systemctl restart caddy || die "Caddy did not start; see journalctl -u caddy"
	fi
	log "Caddy serves https://$1 -> $2 (certificate is issued on first request; DNS for $1 must point here)"
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# 1-2. binaries
if [ -n "$LOCAL_BINARY" ]; then
	[ -x "$LOCAL_BINARY" ] || die "$LOCAL_BINARY is not an executable file"
	cp "$LOCAL_BINARY" "$TMP/atoll-server"
else
	log "downloading atoll-server ($VERSION, linux/$ARCH)"
	fetch "$RELEASE/atoll-server-linux-$ARCH" "$TMP/atoll-server" || die "download failed: $RELEASE/atoll-server-linux-$ARCH"
fi
chmod 0755 "$TMP/atoll-server"
"$TMP/atoll-server" version >/dev/null 2>&1 || die "downloaded atoll-server does not run on this host"
install -m 0755 "$TMP/atoll-server" "$BIN"
log "installed $BIN ($("$BIN" version))"

install -d -m 0700 "$DATA"
if [ "$AGENT_BINARIES" = 1 ]; then
	install -d -m 0755 "$DATA/downloads"
	for a in amd64 arm64; do
		if fetch "$RELEASE/atoll-agent-linux-$a" "$TMP/atoll-agent-linux-$a"; then
			install -m 0755 "$TMP/atoll-agent-linux-$a" "$DATA/downloads/atoll-agent-linux-$a"
		else
			warn "could not mirror atoll-agent-linux-$a; new nodes will download it from GitHub"
		fi
	done
fi

# 3. config + unit
install -d -m 0755 "$(dirname "$ENV_FILE")"
if [ ! -f "$ENV_FILE" ] || [ -n "$PUBLIC_URL" ] || [ "$LISTEN_SET" = 1 ]; then
	if [ -z "$PUBLIC_URL" ] && [ -f "$ENV_FILE" ]; then
		PUBLIC_URL=$(grep -E '^ATOLL_PUBLIC_URL=' "$ENV_FILE" | tail -1 | cut -d= -f2-)
	fi
	if [ "$LISTEN_SET" = 0 ] && [ -f "$ENV_FILE" ]; then
		old_listen=$(grep -E '^ATOLL_LISTEN=' "$ENV_FILE" | tail -1 | cut -d= -f2-)
		[ -z "$old_listen" ] || LISTEN=$old_listen
	fi
	umask 077
	{
		echo "# atoll-server environment (see atoll-server --help)"
		echo "ATOLL_LISTEN=$LISTEN"
		echo "ATOLL_DATA=$DATA"
		if [ -n "$PUBLIC_URL" ]; then echo "ATOLL_PUBLIC_URL=$PUBLIC_URL"; fi
	} >"$ENV_FILE"
	umask 022
	log "wrote $ENV_FILE"
fi

cat >"$UNIT" <<UNITEOF
[Unit]
Description=Atoll control server (REST API, web panel, agent hub)
Documentation=$REPO
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
Environment=ATOLL_LISTEN=:7300
Environment=ATOLL_DATA=$DATA
EnvironmentFile=-$ENV_FILE
ExecStart=$BIN serve
Restart=always
RestartSec=5
TimeoutStopSec=20
LimitNOFILE=65536
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=$DATA
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
UNITEOF

# 4. start
systemctl daemon-reload
systemctl enable atoll-server >/dev/null 2>&1 || true
systemctl restart atoll-server

port=$(printf '%s' "$(grep -E '^ATOLL_LISTEN=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2)" | sed 's/.*://')
[ -n "$port" ] || port=7300
log "waiting for atoll-server on port $port"
i=0
until fetch "http://127.0.0.1:$port/healthz" "$TMP/health" 2>/dev/null; do
	i=$((i + 1))
	if [ $i -ge 30 ]; then
		systemctl status atoll-server --no-pager >&2 || true
		die "atoll-server did not become healthy; see: journalctl -u atoll-server"
	fi
	sleep 1
done

# 5. optional Caddy in front (automatic TLS)
if [ -n "$DOMAIN" ]; then
	setup_caddy "$DOMAIN" "127.0.0.1:$port"
fi

listen=$(grep -E '^ATOLL_LISTEN=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2-)
[ -n "$listen" ] || listen=$LISTEN
url=${PUBLIC_URL:-$(grep -E '^ATOLL_PUBLIC_URL=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2-)}
local_only=0
case "$listen" in 127.* | localhost:* | "[::1]:"*) local_only=1 ;; esac
if [ -z "$url" ]; then
	if [ "$local_only" = 1 ]; then
		url="http://127.0.0.1:$port"
	else
		url="http://$(hostname -I 2>/dev/null | awk '{print $1}'):$port"
	fi
fi
log "atoll-server is running"
{
	echo
	echo "  Open $url to create the owner account (first visit runs setup)."
	if [ "$local_only" = 1 ] && [ -z "$DOMAIN" ] && [ "$url" = "http://127.0.0.1:$port" ]; then
		echo "  atoll-server listens on $listen only: reach it through your TLS proxy, or re-run with --domain NAME."
	elif [ -z "$DOMAIN" ] && [ "$local_only" = 0 ] && [ "${url#https://}" = "$url" ]; then
		echo "  Put a TLS proxy in front before exposing it publicly (or re-run with --domain NAME)."
	fi
	echo "  Forgot the password?  sudo atoll-server reset-password --data $DATA"
	echo
} >&2
