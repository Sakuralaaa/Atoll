#!/bin/sh
# Atoll agent installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-agent.sh \
#     | sudo sh -s -- --server https://atoll.example.com --token atl_join_XXXX [--tailscale-authkey tskey-...]
#
# What it does (safe to re-run):
#   1. installs Docker via get.docker.com (falling back to docker-ce from Docker's apt repo on
#      Debian/Ubuntu), only when Docker is missing. A Zeabur/k3s host keeps
#      k3s running: Docker brings its own containerd, k3s uses its embedded one.
#   2. optionally joins the tailnet (--tailscale-authkey)
#   3. downloads atoll-agent from <server>/downloads/atoll-agent-linux-<arch>, falling back to
#      the latest GitHub release
#   4. runs `atoll-agent join` (skipped when this host is already joined to the same server)
#   5. installs and (re)starts the atoll-agent systemd unit
#
# Options:
#   --server URL                control server URL (required)
#   --token TOKEN               one-time join token atl_join_... (required unless already joined)
#   --tailscale-authkey KEY     install Tailscale if needed and `tailscale up` with this key
#   --rejoin                    join again even if this host is already joined to --server
#   --binary PATH               install this local binary instead of downloading
set -eu

SERVER=""
TOKEN=""
TS_AUTHKEY=""
REJOIN=0
LOCAL_BINARY=""

BIN=${ATOLL_AGENT_BIN:-/usr/local/bin/atoll-agent}
CONFIG=${ATOLL_AGENT_CONFIG:-/etc/atoll/agent.json}
UNIT=${ATOLL_AGENT_UNIT:-/etc/systemd/system/atoll-agent.service}
GITHUB_RELEASE=https://github.com/Sakuralaaa/Atoll/releases/latest/download

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
  --tailscale-authkey KEY     install Tailscale if needed and join the tailnet
  --rejoin                    join again even if already joined to --server
  --binary PATH               install this local atoll-agent binary instead of downloading
USAGE
	exit 2
}

need_arg() {
	[ $# -ge 2 ] && [ -n "$2" ] || die "$1 needs a value"
}

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
	--rejoin)
		REJOIN=1
		shift
		;;
	--binary)
		need_arg "$@"
		LOCAL_BINARY=$2
		shift 2
		;;
	--binary=*)
		LOCAL_BINARY=${1#*=}
		shift
		;;
	-h | --help) usage ;;
	*) die "unknown option: $1 (see --help)" ;;
	esac
done

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
fi

[ "$(uname -s)" = "Linux" ] || die "atoll-agent runs on Linux only"
[ "$(id -u)" -eq 0 ] || die "run as root (pipe into 'sudo sh -s -- ...')"
command -v systemctl >/dev/null 2>&1 || die "systemd is required"

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) die "unsupported architecture $(uname -m) (amd64 and arm64 only)" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --retry 3 --connect-timeout 15 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -T 30 -t 3 -O "$2" "$1"; }
else
	die "curl or wget is required"
fi

TMP=$(mktemp -d 2>/dev/null || mktemp -d -t atoll)
trap 'rm -rf "$TMP"' EXIT
trap 'exit 130' INT TERM

# same_file A B: true when both exist with identical content.
same_file() {
	[ -f "$1" ] && [ -f "$2" ] || return 1
	if command -v cmp >/dev/null 2>&1; then
		cmp -s "$1" "$2"
	else
		[ "$(cksum <"$1")" = "$(cksum <"$2")" ]
	fi
}

# ---------- Docker ----------

docker_ok() { docker info >/dev/null 2>&1; }

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
	apt-get install -y -qq --no-install-recommends docker-ce docker-ce-cli containerd.io >/dev/null || return 1
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

# ---------- Tailscale ----------

setup_tailscale() {
	[ -n "$TS_AUTHKEY" ] || return 0
	if ! command -v tailscale >/dev/null 2>&1; then
		log "installing Tailscale"
		fetch https://tailscale.com/install.sh "$TMP/tailscale.sh" || die "could not download Tailscale installer"
		sh "$TMP/tailscale.sh" || die "Tailscale installation failed"
	fi
	systemctl enable --now tailscaled >/dev/null 2>&1 || true
	if tailscale status >/dev/null 2>&1; then
		log "Tailscale already connected"
		return 0
	fi
	log "joining the tailnet"
	tailscale up --authkey="$TS_AUTHKEY" || die "tailscale up failed"
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
		# The server may answer unknown paths with its web UI, so a download only counts
		# when the file actually runs here.
		if fetch "$SERVER/downloads/$name" "$out" 2>/dev/null; then
			chmod 0755 "$out"
			ver=$("$out" version 2>/dev/null) || ver=""
		fi
		if [ -z "$ver" ]; then
			warn "$SERVER/downloads/$name unavailable; using the latest GitHub release"
			fetch "$GITHUB_RELEASE/$name" "$out" || die "could not download $name"
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

# already_joined: this host's config points at $SERVER with a node token.
already_joined() {
	current=$(configured_server)
	current=${current%/}
	[ "$REJOIN" -eq 0 ] && [ -n "$current" ] && [ "$current" = "$SERVER" ] &&
		grep -q '"node_token"[[:space:]]*:[[:space:]]*"[^"]' "$CONFIG"
}

join() {
	if already_joined; then
		log "already joined to $SERVER (use --rejoin to register again)"
		return
	fi
	[ -n "$TOKEN" ] || die "--token is required to join $SERVER"
	if [ -n "$current" ] && [ "$current" != "$SERVER" ]; then
		warn "this host was joined to $current; re-joining to $SERVER"
	fi
	log "joining $SERVER"
	"$BIN" join --server "$SERVER" --token "$TOKEN" --config "$CONFIG" || die "join failed (tokens are single-use and expire after 1 hour; create a new one in the panel)"
	JOINED=1
}

# ---------- systemd ----------

install_unit() {
	tmpunit="$TMP/atoll-agent.service"
	cat >"$tmpunit" <<'EOF'
[Unit]
Description=Atoll agent (worker node daemon)
Documentation=https://github.com/Sakuralaaa/Atoll
# Containers run in the Docker Engine (they are dockerd's children, not ours, so they keep
# running when the agent stops). The agent also waits for dockerd on its own.
After=network-online.target docker.service
Wants=network-online.target docker.service
# Never give up restarting after a burst of failures.
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart=/usr/local/bin/atoll-agent run --config /etc/atoll/agent.json
Restart=always
RestartSec=5
TimeoutStopSec=30
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
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

# Fail before touching the host when a join is needed but impossible.
if ! already_joined && [ -z "$TOKEN" ]; then
	die "--token is required to join $SERVER"
fi

install_docker
setup_tailscale
install_binary
join
install_unit

log "done: atoll-agent is running and connected to $SERVER (logs: journalctl -u atoll-agent -f)"
