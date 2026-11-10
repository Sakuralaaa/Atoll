#!/bin/sh
# atoll-server container entrypoint: put the agent binaries shipped in the image into the data
# volume (<data>/downloads), where new nodes and agent upgrades fetch them, then run atoll-server.
set -eu
DATA=${ATOLL_DATA:-/var/lib/atoll}
SRC=/opt/atoll/agent
if [ ! -w "$DATA" ]; then
	echo "atoll: $DATA is not writable by uid $(id -u); for a bind mount run: chown -R 10001:10001 <host dir>" >&2
	exit 1
fi
mkdir -p "$DATA/downloads"
for f in "$SRC"/atoll-agent-linux-*; do
	[ -f "$f" ] || continue
	dst="$DATA/downloads/$(basename "$f")"
	if ! cmp -s "$f" "$dst" 2>/dev/null; then
		cp "$f" "$dst.tmp" && chmod 0755 "$dst.tmp" && mv -f "$dst.tmp" "$dst"
	fi
done
case "${1:-}" in
"" | -*) set -- serve "$@" ;;
esac
exec /usr/local/bin/atoll-server "$@"
