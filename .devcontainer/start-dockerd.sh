#!/bin/bash
# Starts a Docker daemon inside the devcontainer without --privileged.
# Called from postStartCommand. Never fails the container start: tests and
# builds do not need Docker, so a problem here only logs a warning.
#
# Under the Kata runtime the container is a microVM, so the guest kernel is
# ours to configure. Three things stand in the way of dockerd:
#   - /var/lib/docker is on virtiofs, which overlayfs cannot use as an upper
#     layer. Fix: an ext4 image in a volume, mounted through a loop device.
#   - /sys/fs/cgroup and /proc/sys are read-only. Fix: remount (needs SYS_ADMIN).
#   - There are no /dev/loop* nodes. Fix: mknod them (needs SYS_ADMIN).
set -u

STORE_DIR=/var/lib/docker-store
STORE_IMG="$STORE_DIR/docker.img"
STORE_SIZE="${DOCKER_STORE_SIZE:-30G}"
LOG=/tmp/dockerd.log

warn() { echo "[dockerd] $*" >&2; exit 0; }

if docker info >/dev/null 2>&1; then
    echo "[dockerd] already running"
    exit 0
fi

command -v dockerd >/dev/null || warn "dockerd not installed, skipping"
[ -d "$STORE_DIR" ] || warn "$STORE_DIR not mounted, skipping"

# Let the remote user talk to the socket (dockerd creates it as root:docker).
sudo usermod -aG docker "$(id -un)" 2>/dev/null || true

sudo mount -o remount,rw /sys/fs/cgroup 2>/dev/null || true
sudo mount -o remount,rw /proc/sys 2>/dev/null || true

if ! mountpoint -q /var/lib/docker; then
    [ -e /dev/loop-control ] || sudo mknod /dev/loop-control c 10 237 || warn "cannot create /dev/loop-control"
    for i in 0 1 2 3 4 5 6 7; do
        [ -e "/dev/loop$i" ] || sudo mknod "/dev/loop$i" b 7 "$i" 2>/dev/null || true
    done
    if [ ! -e "$STORE_IMG" ]; then
        echo "[dockerd] creating $STORE_SIZE sparse store at $STORE_IMG"
        sudo truncate -s "$STORE_SIZE" "$STORE_IMG" || warn "cannot create $STORE_IMG"
        sudo mkfs.ext4 -q -F "$STORE_IMG" || { sudo rm -f "$STORE_IMG"; warn "mkfs.ext4 failed"; }
    fi
    sudo mkdir -p /var/lib/docker
    sudo mount -o loop "$STORE_IMG" /var/lib/docker || warn "loop mount failed; Docker unavailable"
fi

sudo sh -c "nohup dockerd >$LOG 2>&1 &"
for _ in $(seq 1 30); do
    docker info >/dev/null 2>&1 && { echo "[dockerd] started"; exit 0; }
    # The user's new docker group is not in this shell yet; try via sudo.
    sudo docker info >/dev/null 2>&1 && { echo "[dockerd] started"; exit 0; }
    sleep 1
done
warn "daemon did not become ready; see $LOG"
