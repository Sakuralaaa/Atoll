#!/bin/sh
# Atoll agent installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-agent.sh \
#     | sudo sh -s -- --server https://atoll.example.com --token atl_join_XXXX
#
# What it does (safe to re-run):
#   1. asks the control server for its network settings (POST /agent/v1/bootstrap; the join
#      token is checked, not used up)
#   2. installs Docker via get.docker.com (falling back to docker-ce from Docker's apt repo on
#      Debian/Ubuntu), only when Docker is missing. A Zeabur/k3s host keeps
#      k3s running: Docker brings its own containerd, k3s uses its embedded one.
#   3. when the control server uses Tailscale: installs Tailscale, joins the tailnet
#      (--ssh=false --accept-dns=false --accept-routes=false) and registers through the control
#      server's tailnet URL, keeping --server as the fallback. If the tailnet does not work the
#      node joins over --server and can be moved later (`atoll-agent tailnet up`).
#   4. downloads atoll-agent from <server>/downloads/atoll-agent-linux-<arch>, falling back to
#      the latest GitHub release
#   5. runs `atoll-agent join` (skipped when this host is already joined to the same server)
#   6. installs and (re)starts the atoll-agent systemd unit
#
# Options:
#   --server URL                control server URL (required)
#   --token TOKEN               one-time join token atl_join_... (required unless already joined)
#   --tailscale-authkey KEY     use this Tailscale auth key instead of the control server's
#   --tailscale-hostname NAME   tailnet machine name (default atoll-<hostname>)
#   --no-tailnet                never install or use Tailscale; join over --server
#   --rejoin                    join again even if this host is already joined to --server
#   --binary PATH               install this local binary instead of downloading
#   --uninstall                 stop and remove the agent (unit, binary, config); containers
#                               and data stay
#   --purge                     with --uninstall: also remove Atoll's containers and networks
#                               (data under /var/lib/atoll is still kept)
set -eu

SERVER=""
TOKEN=""
TS_AUTHKEY=""
TS_HOSTNAME=""
NO_TAILNET=0
REJOIN=0
LOCAL_BINARY=""
SHA256_AMD64=""
SHA256_ARM64=""
UNINSTALL=0
PURGE=0

BIN=${ATOLL_AGENT_BIN:-/usr/local/bin/atoll-agent}
CONFIG=${ATOLL_AGENT_CONFIG:-/etc/atoll/agent.json}
UNIT=${ATOLL_AGENT_UNIT:-/etc/systemd/system/atoll-agent.service}
GITHUB_RELEASE=https://github.com/Sakuralaaa/Atoll/releases/latest/download
# release tag of the GitHub fallback download ("latest" = newest release); the panel pins it
# to the control server's own version so a fresh node never runs a newer agent than its server
VERSION=latest
# seconds to wait for the control server's tailnet URL after `tailscale up`
PING_WAIT=${ATOLL_PING_WAIT:-60}

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die() {
	printf '\033[1;31merror:\033[0m %s\n' "$*" >&2
	exit 1
}

usage() {
	cat >&2 <<'USAGE'
usage: install-agent.sh --server URL --token atl_join_... [options]

  --server URL                control server URL (required)
  --token TOKEN               one-time join token (required unless already joined)
  --tailscale-authkey KEY     use this Tailscale auth key instead of the control server's
  --tailscale-hostname NAME   tailnet machine name (default atoll-<hostname>)
  --no-tailnet                never install or use Tailscale; join over --server
  --rejoin                    join again even if already joined to --server
  --binary PATH               install this local atoll-agent binary instead of downloading
  --sha256-amd64 HEX          expected SHA-256 of the server-hosted amd64 binary
  --sha256-arm64 HEX          expected SHA-256 of the server-hosted arm64 binary
                              (the panel's "add server" command fills both in)
  --version TAG               release to download when the control server hosts no agent
                              binary (default: latest; the panel pins its own version)
  --uninstall                 stop and remove the agent (unit, binary, config); containers and
                              data stay. Remove the server in the panel too.
  --purge                     with --uninstall: also remove Atoll's containers and networks
                              (data under /var/lib/atoll is kept; delete it by hand)

When the control server has the Tailscale network provider enabled, the node joins the
tailnet automatically and talks to the control server over it (--server stays the fallback).
USAGE
	exit 2
}

need_arg() {
	if [ $# -lt 2 ] || [ -z "$2" ]; then die "$1 needs a value"; fi
}

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--server)
			need_arg "$@"
			SERVER=$2
			shift 2
			;;
		--server=*)
			SERVER=${1#*=}
			shift
			;;
		--token)
			need_arg "$@"
			TOKEN=$2
			shift 2
			;;
		--token=*)
			TOKEN=${1#*=}
			shift
			;;
		--tailscale-authkey)
			need_arg "$@"
			TS_AUTHKEY=$2
			shift 2
			;;
		--tailscale-authkey=*)
			TS_AUTHKEY=${1#*=}
			shift
			;;
		--tailscale-hostname)
			need_arg "$@"
			TS_HOSTNAME=$2
			shift 2
			;;
		--tailscale-hostname=*)
			TS_HOSTNAME=${1#*=}
			shift
			;;
		--no-tailnet)
			NO_TAILNET=1
			shift
			;;
		--rejoin)
			REJOIN=1
			shift
			;;
		--binary)
			need_arg "$@"
			LOCAL_BINARY=$2
			shift 2
			;;
		--sha256-amd64)
			need_arg "$@"
			SHA256_AMD64=$(printf %s "$2" | tr 'A-F' 'a-f')
			shift 2
			;;
		--sha256-arm64)
			need_arg "$@"
			SHA256_ARM64=$(printf %s "$2" | tr 'A-F' 'a-f')
			shift 2
			;;
		--binary=*)
			LOCAL_BINARY=${1#*=}
			shift
			;;
		--version)
			need_arg "$@"
			VERSION=$2
			shift 2
			;;
		--version=*)
			VERSION=${1#*=}
			shift
			;;
		--uninstall)
			UNINSTALL=1
			shift
			;;
		--purge)
			PURGE=1
			shift
			;;
		-h | --help) usage ;;
		*) die "unknown option: $1 (see --help)" ;;
		esac
	done
}

# ---------- helpers ----------

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 --connect-timeout 15 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -T 30 -t 3 -O "$2" "$1"
	else
		die "curl or wget is required"
	fi
}

# sha256_of prints a file's SHA-256 with whichever tool this host has.
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 -r "$1" | awk '{print $1}'
	else
		return 1
	fi
}

# verify_release_file BASE NAME FILE: FILE must match NAME's line in BASE/SHA256SUMS
# (published with every release by scripts/build-release.sh). A mismatch aborts the install.
verify_release_file() {
	sums="$TMP/SHA256SUMS.$(printf %s "$1" | cksum | awk '{print $1}')"
	if [ ! -s "$sums" ]; then
		fetch "$1/SHA256SUMS" "$sums" || die "could not download $1/SHA256SUMS to verify $2"
	fi
	want=$(awk -v n="$2" '$2 == n || $2 == "*" n {print $1; exit}' "$sums")
	[ -n "$want" ] || die "$2 is not listed in $1/SHA256SUMS"
	got=$(sha256_of "$3") || die "need sha256sum, shasum or openssl to verify $2"
	[ "$got" = "$want" ] || die "checksum mismatch for $2: got $got, SHA256SUMS says $want"
	log "verified $2 (sha256 $(printf %.12s "$want")…)"
}

# http_post_json URL BODY OUT: POSTs BODY, writes the response to OUT and prints the HTTP
# status (000 when the server could not be reached; wget only reports 200 or 000).
http_post_json() {
	if command -v curl >/dev/null 2>&1; then
		code=$(curl -sS --retry 2 --connect-timeout 15 -m 60 -o "$3" -w '%{http_code}' \
			-H 'Content-Type: application/json' --data-binary "$2" "$1" 2>/dev/null) || true
		echo "${code:-000}"
	elif wget -q -T 30 -t 2 -O "$3" --header='Content-Type: application/json' --post-data="$2" "$1" 2>/dev/null; then
		echo 200
	else
		echo 000
	fi
}

# probe URL: true when URL/agent/v1/ping answers {"ok":true} (one quick attempt).
probe() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsS --connect-timeout 5 -m 10 -o "$TMP/ping" "$1/agent/v1/ping" 2>/dev/null || return 1
	else
		wget -q -T 10 -t 1 -O "$TMP/ping" "$1/agent/v1/ping" 2>/dev/null || return 1
	fi
	grep -q '"ok"[[:space:]]*:[[:space:]]*true' "$TMP/ping"
}

# wait_ping URL: retry probe for $PING_WAIT seconds (new tailnet peers need a moment).
wait_ping() {
	waited=0
	until probe "$1"; do
		[ "$waited" -lt "$PING_WAIT" ] || return 1
		sleep 2
		waited=$((waited + 2))
	done
}

# same_file A B: true when both exist with identical content.
same_file() {
	[ -f "$1" ] && [ -f "$2" ] || return 1
	if command -v cmp >/dev/null 2>&1; then
		cmp -s "$1" "$2"
	else
		[ "$(cksum <"$1")" = "$(cksum <"$2")" ]
	fi
}

# redact SECRET: copy stdin to stdout with SECRET replaced (no regex, no echo of SECRET).
redact() {
	REDACT_SECRET=$1 awk 'BEGIN { k = ENVIRON["REDACT_SECRET"] }
	{ if (k != "") while ((i = index($0, k)) > 0) $0 = substr($0, 1, i - 1) "[redacted]" substr($0, i + length(k)); print }'
}

# ---------- Docker ----------

docker_ok() { docker info >/dev/null 2>&1; }

# apt_run ARGS...: apt-get without its chatter; on failure shows the last lines (a broken
# mirror, a held package, a full disk) instead of failing silently.
apt_run() {
	aptlog="${TMP:-/tmp}/atoll-apt.log"
	if apt-get "$@" >"$aptlog" 2>&1; then
		return 0
	fi
	warn "apt-get $* failed; last lines:"
	tail -n 8 "$aptlog" | sed 's/^/    /' >&2
	return 1
}

# install_docker_apt installs docker-ce from download.docker.com on Debian/Ubuntu without
# upgrading unrelated packages (get.docker.com can fail when a distro mirror is broken,
# e.g. a 404 on a security update it pulls in).
install_docker_apt() {
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
	apt-get update -qq >/dev/null 2>&1 || true
	command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || apt-get install -y -qq --no-install-recommends curl >/dev/null || return 1
	install -d -m 0755 /etc/apt/keyrings
	fetch "https://download.docker.com/linux/$distro/gpg" /etc/apt/keyrings/docker.asc || return 1
	chmod a+r /etc/apt/keyrings/docker.asc
	echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$distro $codename stable" \
		>/etc/apt/sources.list.d/docker.list
	apt-get update -qq >/dev/null 2>&1 || true
	apt_run install -y -qq --no-install-recommends docker-ce docker-ce-cli containerd.io docker-buildx-plugin || return 1
}

install_docker() {
	if command -v docker >/dev/null 2>&1; then
		if docker_ok; then
			log "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo '') already running"
			return
		fi
		log "Docker is installed but not running; starting it"
		systemctl enable --now docker >/dev/null 2>&1 || systemctl start docker || true
		docker_ok || die "Docker is installed but the daemon does not start; check 'systemctl status docker'"
		return
	fi
	if [ -x /usr/local/bin/k3s ] || command -v k3s >/dev/null 2>&1; then
		log "k3s found (Zeabur?): it keeps running; Docker is installed alongside with its own containerd"
	fi
	log "installing Docker via get.docker.com"
	if ! { fetch https://get.docker.com "$TMP/get-docker.sh" && sh "$TMP/get-docker.sh"; }; then
		warn "get.docker.com failed; trying docker-ce from Docker's apt repository"
		install_docker_apt || die "Docker installation failed (get.docker.com and the Docker apt repository both failed)"
	fi
	systemctl enable --now docker >/dev/null 2>&1 || true
	i=0
	until docker_ok; do
		i=$((i + 1))
		[ $i -le 30 ] || die "Docker did not start after installation"
		sleep 2
	done
	log "Docker installed"
}

# install_git: builds from source run `git` on this server (best effort: only builds need it).
# git is optional (only builds from source need it): a broken package mirror must not stop the install.
install_git() {
	command -v git >/dev/null 2>&1 && return
	log "installing git (used by builds from source)"
	if command -v apt-get >/dev/null 2>&1; then
		apt-get install -y -qq --no-install-recommends git ca-certificates >/dev/null 2>&1 ||
			{ apt-get update -qq >/dev/null 2>&1; apt_run install -y -qq --no-install-recommends git ca-certificates; } || true
	elif command -v dnf >/dev/null 2>&1; then
		dnf install -y -q git >/dev/null 2>&1 || true
	elif command -v yum >/dev/null 2>&1; then
		yum install -y -q git >/dev/null 2>&1 || true
	elif command -v apk >/dev/null 2>&1; then
		apk add --no-cache git >/dev/null 2>&1 || true
	fi
	command -v git >/dev/null 2>&1 || warn "could not install git; builds from source on this server will fail until it is installed"
}

# ---------- Tailscale ----------
# install_tailscale_apt / install_tailscale / tailscale_* are identical in install-server.sh
# (checked by scripts/test-installers.sh) and follow internal/agent/tailnet/install-tailscale.sh.

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

# ---------- control server network (bootstrap) ----------

NET_PROVIDER=""
NET_AUTHKEY=""
NET_CONTROL_URL=""
JOIN_URL=""
FALLBACK_URL=""

# env_value KEY FILE: value of KEY=value in FILE (never sourced).
env_value() {
	sed -n "s/^$1=//p" "$2" | head -n 1 | tr -d '\r'
}

# bootstrap: POST /agent/v1/bootstrap?format=env with the join token (checked, not consumed).
bootstrap() {
	[ -n "$TOKEN" ] || return 0
	out="$TMP/bootstrap.env"
	code=$(http_post_json "$SERVER/agent/v1/bootstrap?format=env" "{\"join_token\":\"$TOKEN\"}" "$out")
	case "$code" in
	200) ;;
	401 | 403) die "the join token is invalid, expired or already used (tokens are single-use and expire after 1 hour; create a new one in the panel)" ;;
	404 | 405)
		warn "$SERVER has no /agent/v1/bootstrap (older control server); joining over $SERVER"
		return 0
		;;
	*)
		warn "could not ask $SERVER for its network settings (HTTP $code); joining over $SERVER"
		return 0
		;;
	esac
	if ! grep -q '^ATOLL_NETWORK=' "$out"; then
		warn "$SERVER answered /agent/v1/bootstrap with something unexpected; joining over $SERVER"
		return 0
	fi
	NET_PROVIDER=$(env_value ATOLL_NETWORK "$out")
	NET_AUTHKEY=$(env_value ATOLL_TS_AUTHKEY "$out")
	NET_CONTROL_URL=$(env_value ATOLL_CONTROL_URL "$out")
	NET_CONTROL_URL=${NET_CONTROL_URL%/}
	rm -f "$out"
	case "$NET_CONTROL_URL" in "" | http://* | https://*) ;; *) NET_CONTROL_URL="" ;; esac
	if [ "$NET_PROVIDER" = tailscale ]; then
		log "the control server uses Tailscale${NET_CONTROL_URL:+ (agent endpoint $NET_CONTROL_URL)}"
	fi
}

# setup_network: join the tailnet when the control server (or --tailscale-authkey) asks for it
# and pick the URL to join through. Any tailnet problem falls back to $SERVER with a warning.
setup_network() {
	JOIN_URL=$SERVER
	FALLBACK_URL=""
	[ "$NO_TAILNET" = 0 ] || return 0
	[ "$NET_PROVIDER" = tailscale ] || [ -n "$TS_AUTHKEY" ] || return 0
	key=${TS_AUTHKEY:-$NET_AUTHKEY}
	if [ -z "$key" ] && ! tailscale_running; then
		warn "the control server uses Tailscale but sent no auth key (Settings → Private network); joining over $SERVER"
		return 0
	fi
	if ! install_tailscale; then
		warn "Tailscale installation failed; joining over $SERVER (later: atoll-agent tailnet up)"
		return 0
	fi
	if ! tailscale_up "$key" "${TS_HOSTNAME:-$(tailscale_hostname "$(hostname)")}"; then
		warn "could not join the tailnet; joining over $SERVER (later: atoll-agent tailnet up)"
		return 0
	fi
	log "on the tailnet as $(tailscale_ip)"
	if [ -z "$NET_CONTROL_URL" ]; then
		if [ "$NET_PROVIDER" = tailscale ]; then
			warn "the control server has no tailnet URL yet (ATOLL_AGENT_LISTEN); joining over $SERVER — the agent moves to the tailnet once it has one"
		fi
		return 0
	fi
	[ "$NET_CONTROL_URL" != "$SERVER" ] || return 0
	log "waiting for the control server at $NET_CONTROL_URL"
	if wait_ping "$NET_CONTROL_URL"; then
		JOIN_URL=$NET_CONTROL_URL
		FALLBACK_URL=$SERVER
	else
		warn "$NET_CONTROL_URL is not reachable over the tailnet (Tailscale ACL must allow tag:atoll -> tag:atoll:7301); joining over $SERVER"
	fi
}

# ---------- binary ----------

install_binary() {
	out="$TMP/atoll-agent"
	if [ -n "$LOCAL_BINARY" ]; then
		[ -f "$LOCAL_BINARY" ] || die "--binary $LOCAL_BINARY not found"
		cp "$LOCAL_BINARY" "$out"
		chmod 0755 "$out"
		ver=$("$out" version 2>/dev/null) || die "$LOCAL_BINARY does not run on this host ($ARCH)"
	else
		name="atoll-agent-linux-$ARCH"
		ver=""
		log "downloading $name"
		pin=""
		case "$ARCH" in
		amd64) pin=$SHA256_AMD64 ;;
		arm64) pin=$SHA256_ARM64 ;;
		esac
		# The server may answer unknown paths with its web UI, so a download only counts
		# when the file actually runs here. With a pinned checksum it must match before it runs.
		if fetch "$SERVER/downloads/$name" "$out" 2>/dev/null; then
			if [ -n "$pin" ]; then
				got=$(sha256_of "$out") || die "need sha256sum, shasum or openssl to verify $name"
				[ "$got" = "$pin" ] || die "checksum mismatch for $SERVER/downloads/$name: got $got, expected $pin (refusing to install it)"
				log "verified $name against the pinned SHA-256"
			else
				warn "$SERVER/downloads/$name is not verified (no --sha256-$ARCH given)"
			fi
			chmod 0755 "$out"
			ver=$("$out" version 2>/dev/null) || ver=""
		fi
		if [ -z "$ver" ]; then
			rel=$GITHUB_RELEASE
			if [ "$VERSION" != latest ]; then
				rel="https://github.com/Sakuralaaa/Atoll/releases/download/$VERSION"
			fi
			warn "$SERVER/downloads/$name unavailable; using GitHub release $VERSION"
			fetch "$rel/$name" "$out" || die "could not download $name ($VERSION)"
			verify_release_file "$rel" "$name" "$out"
			chmod 0755 "$out"
			ver=$("$out" version 2>/dev/null) || die "downloaded atoll-agent does not run on this host ($ARCH)"
		fi
	fi
	if [ -x "$BIN" ] && same_file "$out" "$BIN"; then
		log "atoll-agent $ver already installed"
		BINARY_CHANGED=0
		return
	fi
	mkdir -p "$(dirname "$BIN")"
	# Same-filesystem rename: atomic, and safe while the old binary is running.
	cp "$out" "$BIN.new"
	chmod 0755 "$BIN.new"
	mv -f "$BIN.new" "$BIN"
	BINARY_CHANGED=1
	log "installed atoll-agent $ver to $BIN"
}

# ---------- join ----------

configured_server() {
	[ -f "$CONFIG" ] || return 0
	sed -n 's/.*"server_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG" | head -n 1
}

# config_urls: server_url and every fallback_urls entry of $CONFIG, one per line.
config_urls() {
	[ -f "$CONFIG" ] || return 0
	awk '/"server_url"[[:space:]]*:/ { print; next }
		/"fallback_urls"[[:space:]]*:/ { f = 1 }
		f { print }
		f && /\]/ { f = 0 }' "$CONFIG" | grep -o 'https\{0,1\}://[^"]*' | sed 's#/*$##'
}

# already_joined: this host's config has a node token and $SERVER is its control URL or one
# of its fallbacks (after moving to the tailnet the public URL is a fallback).
already_joined() {
	[ "$REJOIN" -eq 0 ] || return 1
	[ -f "$CONFIG" ] && grep -q '"node_token"[[:space:]]*:[[:space:]]*"[^"]' "$CONFIG" || return 1
	for u in $(config_urls); do
		[ "$u" = "$SERVER" ] && return 0
	done
	return 1
}

# add_fallback URL: keep URL in the agent config's fallback_urls (the agent tries it when the
# primary control URL keeps failing).
add_fallback() {
	case "$1" in *[\"\\\&]* | "") return 0 ;; esac
	for u in $(config_urls); do
		[ "$u" = "$1" ] && return 0
	done
	tmpcfg="$CONFIG.tmp.$$"
	if grep -q '"fallback_urls"' "$CONFIG"; then
		awk -v u="$1" '!done && /"fallback_urls"[[:space:]]*:[[:space:]]*\[/ {
			sub(/\[/, "[\"" u "\"" ($0 ~ /\[[[:space:]]*\]/ ? "" : ","))
			done = 1
		} { print }' "$CONFIG" >"$tmpcfg"
	else
		awk -v u="$1" '{ print } !done && /"server_url"[[:space:]]*:/ {
			print "  \"fallback_urls\": [\"" u "\"],"
			done = 1
		}' "$CONFIG" >"$tmpcfg"
	fi
	chmod 0600 "$tmpcfg"
	mv -f "$tmpcfg" "$CONFIG"
}

join() {
	if already_joined; then
		log "already joined to $SERVER (use --rejoin to register again)"
		return
	fi
	[ -n "$TOKEN" ] || die "--token is required to join $SERVER"
	current=$(configured_server)
	current=${current%/}
	if [ -n "$current" ] && [ "$current" != "$JOIN_URL" ]; then
		warn "this host was joined to $current; re-joining to $JOIN_URL"
	fi
	log "joining $JOIN_URL"
	if ! "$BIN" join --server "$JOIN_URL" --token "$TOKEN" --config "$CONFIG"; then
		if [ "$JOIN_URL" = "$SERVER" ] && [ "$NET_PROVIDER" = tailscale ]; then
			die "join failed. If the control server only serves agents on the tailnet (ATOLL_AGENT_TAILNET_ONLY=1), this node must reach it over Tailscale first; otherwise the token may be used up (create a new one in the panel)"
		fi
		die "join failed (tokens are single-use and expire after 1 hour; create a new one in the panel)"
	fi
	if [ -n "$FALLBACK_URL" ]; then
		add_fallback "$FALLBACK_URL"
		log "control URL $JOIN_URL (fallback $FALLBACK_URL)"
	fi
	JOINED=1
}

# ---------- systemd ----------

# write_unit FILE: the atoll-agent unit; ordered after tailscaled when Tailscale is installed.
write_unit() {
	after="network-online.target docker.service"
	if command -v tailscale >/dev/null 2>&1; then
		after="$after tailscaled.service"
	fi
	cat >"$1" <<EOF
[Unit]
Description=Atoll agent (worker node daemon)
Documentation=https://github.com/Sakuralaaa/Atoll
# Containers run in the Docker Engine (they are dockerd's children, not ours, so they keep
# running when the agent stops). The agent also waits for dockerd on its own.
# tailscaled: the control URL may be a tailnet address.
After=$after
Wants=$after
# Never give up restarting after a burst of failures.
StartLimitIntervalSec=0

[Service]
Type=simple
# optional overrides, e.g. ATOLL_BUILD_MEMORY=2g (see README "Builds from source")
EnvironmentFile=-/etc/atoll/agent.env
ExecStart=/usr/local/bin/atoll-agent run --config /etc/atoll/agent.json
Restart=always
RestartSec=5
TimeoutStopSec=30
LimitNOFILE=1048576
# Keep the agent alive under memory pressure: the OOM killer picks a service container first.
OOMScoreAdjust=-500

[Install]
WantedBy=multi-user.target
EOF
}

install_unit() {
	tmpunit="$TMP/atoll-agent.service"
	write_unit "$tmpunit"
	UNIT_CHANGED=0
	if ! same_file "$tmpunit" "$UNIT"; then
		cp "$tmpunit" "$UNIT"
		chmod 0644 "$UNIT"
		UNIT_CHANGED=1
	fi
	systemctl daemon-reload
	systemctl enable atoll-agent >/dev/null 2>&1
	if [ "$BINARY_CHANGED" -eq 1 ] || [ "$UNIT_CHANGED" -eq 1 ] || [ "$JOINED" -eq 1 ] ||
		! systemctl is-active --quiet atoll-agent; then
		log "starting atoll-agent"
		systemctl restart atoll-agent
	fi
	sleep 2
	systemctl is-active --quiet atoll-agent || die "atoll-agent did not start; see 'journalctl -u atoll-agent'"
}

BINARY_CHANGED=0
JOINED=0
UNIT_CHANGED=0

# uninstall stops and removes the agent. Containers keep running unless --purge.
uninstall() {
	[ "$(id -u)" = 0 ] || die "run as root (sudo)"
	log "stopping atoll-agent"
	systemctl disable --now atoll-agent >/dev/null 2>&1 || true
	rm -f "$UNIT"
	systemctl daemon-reload >/dev/null 2>&1 || true
	rm -f "$BIN" "$BIN.prev" "$BIN.new"
	rm -f "$CONFIG" "$(dirname "$CONFIG")/agent.env"
	rmdir "$(dirname "$CONFIG")" 2>/dev/null || true
	if [ "$PURGE" = 1 ] && command -v docker >/dev/null 2>&1; then
		ids=$(docker ps -aq --filter label=atoll.managed=true 2>/dev/null || true)
		if [ -n "$ids" ]; then
			log "removing Atoll containers"
			# shellcheck disable=SC2086
			docker rm -f $ids >/dev/null || warn "could not remove some containers"
		fi
		for n in $(docker network ls -q --filter label=atoll.managed=true 2>/dev/null || true) $(docker network ls --format '{{.Name}}' 2>/dev/null | grep '^atoll-' || true); do
			docker network rm "$n" >/dev/null 2>&1 || true
		done
		log "data under /var/lib/atoll was kept; delete it yourself when you no longer need it"
	elif [ "$PURGE" = 0 ]; then
		log "containers and data were kept (use --uninstall --purge to remove Atoll's containers)"
	fi
	log "done: remove this server in the panel (Servers) as well"
}

main() {
	parse_args "$@"
	if [ "$UNINSTALL" = 1 ]; then
		uninstall
		return
	fi
	[ "$PURGE" = 0 ] || die "--purge only works with --uninstall"

	# ---------- preflight ----------
	[ -n "$SERVER" ] || die "--server is required"
	SERVER=${SERVER%/}
	case "$SERVER" in
	http://* | https://*) ;;
	*) die "--server must start with http:// or https:// (got $SERVER)" ;;
	esac
	case "$SERVER" in
	http://*) warn "--server uses plain http; the node token will travel unencrypted" ;;
	esac
	if [ -n "$TOKEN" ]; then
		case "$TOKEN" in
		atl_join_*) ;;
		*) die "--token must be a join token (atl_join_...)" ;;
		esac
		case "$TOKEN" in *[!A-Za-z0-9_-]*) die "--token contains unexpected characters" ;; esac
	fi
	if [ -n "$TS_AUTHKEY" ]; then
		case "$TS_AUTHKEY" in tskey-*) ;; *) die "--tailscale-authkey must be a Tailscale auth key (tskey-auth-...)" ;; esac
	fi

	[ "$(uname -s)" = "Linux" ] || die "atoll-agent runs on Linux only"
	[ "$(id -u)" -eq 0 ] || die "run as root (pipe into 'sudo sh -s -- ...')"
	command -v systemctl >/dev/null 2>&1 || die "systemd is required"
	command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || die "curl or wget is required"

	case "$(uname -m)" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) die "unsupported architecture $(uname -m) (amd64 and arm64 only)" ;;
	esac

	TMP=$(mktemp -d 2>/dev/null || mktemp -d -t atoll)
	trap 'rm -rf "$TMP"' EXIT
	trap 'exit 130' INT TERM

	JOIN_URL=$SERVER
	if already_joined; then
		# The agent manages its own network from here (panel → Servers → Join tailnet, or
		# `atoll-agent tailnet up`); only an explicit --tailscale-authkey is acted on.
		NEED_JOIN=0
	else
		# Fail before touching the host when a join is needed but impossible.
		[ -n "$TOKEN" ] || die "--token is required to join $SERVER"
		NEED_JOIN=1
		bootstrap
	fi

	install_docker
	install_git
	if [ "$NEED_JOIN" = 1 ]; then
		setup_network
	elif [ -n "$TS_AUTHKEY" ] && [ "$NO_TAILNET" = 0 ]; then
		if install_tailscale && tailscale_up "$TS_AUTHKEY" "${TS_HOSTNAME:-$(tailscale_hostname "$(hostname)")}"; then
			log "on the tailnet as $(tailscale_ip); move the agent with: atoll-agent tailnet up"
		else
			warn "could not join the tailnet"
		fi
	fi
	install_binary
	join
	install_unit

	log "done: atoll-agent is running and connected to $(configured_server) (logs: journalctl -u atoll-agent -f)"
}

# ATOLL_INSTALL_SOURCED=1 only defines the functions (scripts/test-installers.sh).
[ "${ATOLL_INSTALL_SOURCED:-0}" = 1 ] || main "$@"
