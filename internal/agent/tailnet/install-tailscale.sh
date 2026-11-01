#!/bin/sh
# Installs Tailscale from the official sources and enables tailscaled. Safe to re-run.
#   1. https://tailscale.com/install.sh (official repository setup script)
#   2. fallback on Debian/Ubuntu: the official apt repository at pkgs.tailscale.com
# Keep in sync with install_tailscale() in install/install-agent.sh and install-server.sh.
set -eu

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 --connect-timeout 15 -o "$2" "$1"
	else
		wget -q -T 30 -t 3 -O "$2" "$1"
	fi
}

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

if ! command -v tailscale >/dev/null 2>&1; then
	tmp=$(mktemp)
	if ! { fetch https://tailscale.com/install.sh "$tmp" && sh "$tmp"; }; then
		echo "tailscale.com/install.sh failed; trying the official apt repository" >&2
		install_tailscale_apt || {
			rm -f "$tmp"
			echo "Tailscale installation failed" >&2
			exit 1
		}
	fi
	rm -f "$tmp"
fi
systemctl enable --now tailscaled >/dev/null 2>&1 || systemctl start tailscaled || true
command -v tailscale >/dev/null 2>&1
