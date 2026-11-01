#!/bin/sh
# Atoll control server installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-server.sh \
#     | sudo sh -s -- [--domain atoll.example.com] [--public-url URL] [--listen :7300] [--version v0.1.0] \
#       [--tailscale-authkey tskey-auth-... [--agent-tailnet-only]]
#
# What it does (safe to re-run; upgrades in place):
#   1. detects the architecture (amd64 / arm64)
#   2. downloads atoll-server (and atoll-agent binaries to serve to new nodes) from GitHub releases
#   3. writes /etc/atoll/server.env and the atoll-server systemd unit (data dir /var/lib/atoll)
#   4. (re)starts the service and waits for /healthz
#   5. with --domain: installs Caddy and serves the panel on https://DOMAIN (automatic TLS)
#   6. with --tailscale-authkey: installs Tailscale, joins the tailnet as atoll-control
#      (--ssh=false --accept-dns=false --accept-routes=false) and serves the agent endpoints on
#      this host's tailnet IP (ATOLL_AGENT_LISTEN=tailscale:7301)
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
#   --tailscale-authkey KEY  install Tailscale and join the tailnet with KEY (reusable,
#                      pre-approved, tag:atoll); agents then reach this server on
#                      http://<tailnet IP>:7301. Paste the same key in Settings → Private
#                      network so new nodes get it. The key is not stored by this script.
#   --tailscale-hostname NAME  tailnet machine name (default atoll-control)
#   --agent-tailnet-only  stop answering /agent/* on the public URL (ATOLL_AGENT_TAILNET_ONLY=1;
#                      POST /agent/v1/bootstrap stays public so new nodes can join)
set -eu

PUBLIC_URL=""
DOMAIN=""
LISTEN=":7300"
LISTEN_SET=0
VERSION="latest"
LOCAL_BINARY=""
AGENT_BINARIES=1
TS_AUTHKEY=""
TS_HOSTNAME="atoll-control"
AGENT_TAILNET_ONLY=0
AGENT_LISTEN_DEFAULT="tailscale:7301"

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
	--tailscale-authkey) need_arg "$@"; TS_AUTHKEY=$2; shift 2 ;;
	--tailscale-authkey=*) TS_AUTHKEY=${1#*=}; shift ;;
	--tailscale-hostname) need_arg "$@"; TS_HOSTNAME=$2; shift 2 ;;
	--tailscale-hostname=*) TS_HOSTNAME=${1#*=}; shift ;;
	--agent-tailnet-only) AGENT_TAILNET_ONLY=1; shift ;;
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

if [ -n "$TS_AUTHKEY" ]; then
	case "$TS_AUTHKEY" in tskey-*) ;; *) die "--tailscale-authkey must be a Tailscale auth key (tskey-auth-...)" ;; esac
fi
case "$TS_HOSTNAME" in "" | *[!a-z0-9-]* | -*) die "--tailscale-hostname must be lowercase letters, digits and dashes" ;; esac

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

# setup_caddy DOMAIN UPSTREAM [tailnet-only]: install Caddy (Debian/Ubuntu package, else
# Caddy's apt repo) and add a site block for DOMAIN. An existing Caddyfile that is not the stock
# one is kept; the block is appended once. With tailnet-only the site answers /agent/* with 404
# (agents use the tailnet listener), except POST /agent/v1/bootstrap which new nodes need.
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
	agent_guard=""
	if [ "${3:-}" = tailnet-only ]; then
		agent_guard="	# ATOLL_AGENT_TAILNET_ONLY=1: agents talk to the tailnet listener
	@agent {
		path /agent/*
		not path /agent/v1/bootstrap
	}
	respond @agent 404
"
	fi
	block="# atoll panel (install-server.sh --domain)
$1 {
${agent_guard}	reverse_proxy $2
}"
	if [ ! -s "$CADDYFILE" ] || grep -q '^# atoll-managed' "$CADDYFILE" || grep -q '/usr/share/caddy' "$CADDYFILE"; then
		printf '# atoll-managed: rewritten by install-server.sh --domain\n%s\n' "$block" >"$CADDYFILE"
	elif grep -Eq "^$1([ ,]|\$)" "$CADDYFILE"; then
		log "$CADDYFILE already has a site block for $1; leaving it as is"
		if [ "${3:-}" = tailnet-only ]; then
			warn "add to the $1 block by hand: @agent { path /agent/*; not path /agent/v1/bootstrap } + respond @agent 404"
		fi
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

# ---------- Tailscale (shared with install-agent.sh) ----------
install_tailscale_apt() {
	command -v apt-get >/dev/null 2>&1 || return 1
	[ -r /etc/os-release ] || return 1
	# shellcheck disable=SC1091
	. /etc/os-release
	case "${ID:-}" in
	debian | ubuntu) distro=$ID ;;
	*) case "${ID_LIKE:-}" in *ubuntu*) distro=ubuntu ;; *debian*) distro=debian ;; *) return 1 ;; esac ;;
	esac
	codename=${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}
	[ -n "$codename" ] || return 1
	export DEBIAN_FRONTEND=noninteractive
	install -d -m 0755 /usr/share/keyrings
	fetch "https://pkgs.tailscale.com/stable/$distro/$codename.noarmor.gpg" /usr/share/keyrings/tailscale-archive-keyring.gpg || return 1
	fetch "https://pkgs.tailscale.com/stable/$distro/$codename.tailscale-keyring.list" /etc/apt/sources.list.d/tailscale.list || return 1
	apt-get update -qq >/dev/null 2>&1 || true
	apt-get install -y -qq tailscale >/dev/null || return 1
}

# install_tailscale: official install script, else the official apt repository; enables
# tailscaled. Does nothing but (re)start tailscaled when Tailscale is already installed.
install_tailscale() {
	if ! command -v tailscale >/dev/null 2>&1; then
		log "installing Tailscale"
		if ! { fetch https://tailscale.com/install.sh "$TMP/tailscale-install.sh" && sh "$TMP/tailscale-install.sh"; }; then
			warn "tailscale.com/install.sh failed; trying the official apt repository"
			install_tailscale_apt || return 1
		fi
	fi
	systemctl enable --now tailscaled >/dev/null 2>&1 || systemctl start tailscaled >/dev/null 2>&1 || true
	command -v tailscale >/dev/null 2>&1
}

# tailscale_is_ip IP: true for an address in 100.64.0.0/10 (Tailscale's CGNAT range).
tailscale_is_ip() {
	case "$1" in 100.*.*.*) ;; *) return 1 ;; esac
	o=${1#100.}
	o=${o%%.*}
	case "$o" in '' | *[!0-9]*) return 1 ;; esac
	[ "$o" -ge 64 ] && [ "$o" -le 127 ]
}

tailscale_ip() {
	command -v tailscale >/dev/null 2>&1 || return 0
	tailscale ip -4 2>/dev/null | head -n 1
}

# tailscale_running: logged in, Running, with a tailnet IPv4.
tailscale_running() {
	command -v tailscale >/dev/null 2>&1 || return 1
	tailscale status --json 2>/dev/null | grep -q '"BackendState"[[:space:]]*:[[:space:]]*"Running"' || return 1
	tailscale_is_ip "$(tailscale_ip)"
}

# tailscale_hostname NAME: atoll-<name> as a valid tailnet machine name.
tailscale_hostname() {
	h=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -e 's/[^a-z0-9-][^a-z0-9-]*/-/g' -e 's/^-*//' -e 's/-*$//')
	[ -n "$h" ] || h=node
	case "$h" in atoll-*) ;; *) h="atoll-$h" ;; esac
	printf '%s\n' "$h" | cut -c1-63 | sed 's/-*$//'
}

# tailscale_up KEY HOSTNAME: `tailscale up` with Atoll's flags unless this host already is on a
# tailnet (an existing setup is never logged in again). The key is never printed.
tailscale_up() {
	if tailscale_running; then
		log "Tailscale already up ($(tailscale_ip)); not logging in again"
		return 0
	fi
	[ -n "$1" ] || {
		warn "Tailscale is not logged in and no auth key is available"
		return 1
	}
	log "joining the tailnet as $2"
	if ! tailscale up --authkey="$1" --hostname="$2" --ssh=false --accept-dns=false --accept-routes=false >"$TMP/tailscale-up.log" 2>&1; then
		if grep -q 'requires mentioning all non-default flags' "$TMP/tailscale-up.log" &&
			tailscale up --authkey="$1" --hostname="$2" --ssh=false --accept-dns=false --accept-routes=false --reset >"$TMP/tailscale-up.log" 2>&1; then
			:
		else
			tail -n 20 "$TMP/tailscale-up.log" | redact "$1" >&2
			rm -f "$TMP/tailscale-up.log"
			warn "tailscale up failed"
			return 1
		fi
	fi
	rm -f "$TMP/tailscale-up.log"
	i=0
	until tailscale_running; do
		i=$((i + 1))
		[ $i -le 30 ] || {
			warn "Tailscale did not reach Running"
			return 1
		}
		sleep 2
	done
}

# ---------- end of the shared Tailscale functions ----------

# redact SECRET: copy stdin to stdout with SECRET replaced (no regex, no echo of SECRET).
redact() {
	REDACT_SECRET=$1 awk 'BEGIN { k = ENVIRON["REDACT_SECRET"] }
	{ if (k != "") while ((i = index($0, k)) > 0) $0 = substr($0, 1, i - 1) "[redacted]" substr($0, i + length(k)); print }'
}

# env_get KEY: last value of KEY in $ENV_FILE.
env_get() {
	[ -f "$ENV_FILE" ] || return 0
	grep -E "^$1=" "$ENV_FILE" | tail -n 1 | cut -d= -f2-
}

# set_env KEY VALUE: replace KEY in $ENV_FILE, keeping every other line (and the file mode).
set_env() {
	grep -v -E "^$1=" "$ENV_FILE" >"$TMP/env.new" || true
	printf '%s=%s\n' "$1" "$2" >>"$TMP/env.new"
	cat "$TMP/env.new" >"$ENV_FILE"
	rm -f "$TMP/env.new"
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [ "$AGENT_TAILNET_ONLY" = 1 ] && [ -z "$TS_AUTHKEY" ] && [ -z "$(env_get ATOLL_AGENT_LISTEN)" ]; then
	die "--agent-tailnet-only needs the tailnet listener: add --tailscale-authkey KEY (or set ATOLL_AGENT_LISTEN in $ENV_FILE)"
fi

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

# 3. Tailscale on the control server (agents reach it over the tailnet)
if [ -n "$TS_AUTHKEY" ]; then
	install_tailscale || die "Tailscale installation failed (tailscale.com/install.sh and the official apt repository)"
	tailscale_up "$TS_AUTHKEY" "$TS_HOSTNAME" || die "could not join the tailnet (check that the auth key is valid, reusable and pre-approved)"
	log "on the tailnet as $(tailscale_ip)"
fi

# 4. config + unit. Lines this script does not manage (ATOLL_LOG_LEVEL, ...) are kept.
install -d -m 0755 "$(dirname "$ENV_FILE")"
env_changed=0
if [ ! -f "$ENV_FILE" ]; then
	(umask 077 && echo "# atoll-server environment (see atoll-server --help)" >"$ENV_FILE")
	env_changed=1
fi
chmod 0600 "$ENV_FILE"
if [ "$LISTEN_SET" = 1 ] || [ -z "$(env_get ATOLL_LISTEN)" ]; then
	set_env ATOLL_LISTEN "$LISTEN"
	env_changed=1
fi
if [ -z "$(env_get ATOLL_DATA)" ]; then
	set_env ATOLL_DATA "$DATA"
	env_changed=1
fi
if [ -n "$PUBLIC_URL" ] && [ "$(env_get ATOLL_PUBLIC_URL)" != "$PUBLIC_URL" ]; then
	set_env ATOLL_PUBLIC_URL "$PUBLIC_URL"
	env_changed=1
fi
if [ -n "$TS_AUTHKEY" ]; then
	cur=$(env_get ATOLL_AGENT_LISTEN)
	if [ -z "$cur" ]; then
		set_env ATOLL_AGENT_LISTEN "$AGENT_LISTEN_DEFAULT"
		env_changed=1
	elif [ "$cur" != "$AGENT_LISTEN_DEFAULT" ]; then
		log "keeping ATOLL_AGENT_LISTEN=$cur from $ENV_FILE"
	fi
fi
if [ "$AGENT_TAILNET_ONLY" = 1 ] && [ "$(env_get ATOLL_AGENT_TAILNET_ONLY)" != 1 ]; then
	set_env ATOLL_AGENT_TAILNET_ONLY 1
	env_changed=1
fi
[ "$env_changed" = 0 ] || log "updated $ENV_FILE"
AGENT_LISTEN=$(env_get ATOLL_AGENT_LISTEN)
case "$(env_get ATOLL_AGENT_TAILNET_ONLY)" in 1 | true | yes | on) AGENT_TAILNET_ONLY=1 ;; esac

# The tailnet listener (tailscale:PORT) waits for tailscaled's IP; order the unit after it.
unit_after="network-online.target"
if command -v tailscale >/dev/null 2>&1; then
	unit_after="$unit_after tailscaled.service"
fi
cat >"$UNIT" <<UNITEOF
[Unit]
Description=Atoll control server (REST API, web panel, agent hub)
Documentation=$REPO
After=$unit_after
Wants=$unit_after
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

# 5. start
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

# 6. optional Caddy in front (automatic TLS)
if [ -n "$DOMAIN" ]; then
	if [ "$AGENT_TAILNET_ONLY" = 1 ]; then
		setup_caddy "$DOMAIN" "127.0.0.1:$port" tailnet-only
	else
		setup_caddy "$DOMAIN" "127.0.0.1:$port"
	fi
fi

# 7. the tailnet agent listener
tailnet_url=""
if [ -n "$AGENT_LISTEN" ]; then
	ts_ip=$(tailscale_ip)
	ts_port=${AGENT_LISTEN##*:}
	if tailscale_is_ip "$ts_ip"; then
		tailnet_url="http://$ts_ip:$ts_port"
		i=0
		until fetch "$tailnet_url/agent/v1/ping" "$TMP/ping" 2>/dev/null && grep -q '"ok"' "$TMP/ping"; do
			i=$((i + 1))
			if [ $i -ge 20 ]; then
				warn "agents cannot reach $tailnet_url/agent/v1/ping yet; see: journalctl -u atoll-server"
				break
			fi
			sleep 1
		done
	else
		warn "ATOLL_AGENT_LISTEN=$AGENT_LISTEN but this host has no tailnet IP (tailscale status)"
	fi
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
	if [ -n "$tailnet_url" ]; then
		echo "  Agents reach this server over the tailnet at $tailnet_url."
		echo "  Settings → Private network: choose Tailscale and paste the same auth key, so new"
		echo "  nodes join the tailnet (tailnet control URL: $tailnet_url)."
		if [ "$AGENT_TAILNET_ONLY" = 1 ]; then
			echo "  /agent/* is not answered on the public URL (ATOLL_AGENT_TAILNET_ONLY=1); only bootstrap is."
		fi
	fi
	echo "  Forgot the password?  sudo atoll-server reset-password --data $DATA"
	echo
} >&2
