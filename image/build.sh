#!/bin/sh
# build.sh: build the box image from scratch and export it to
# $PUNCHCLOCK_HOME/image/rootfs.ext4, on a Linux build machine.
#
# The context is this directory plus what boxes take from the build machine:
# the punchclock and ferry it runs (ferry push keeps ferry in step with the
# laptop), and hallpass's client binary and config, since boxes share its
# broker. Claude, Codex, and Rex install fresh, so rebuilding picks up new
# versions. podman owns /etc/hosts and /etc/hostname at build time, so they're
# written at export; each box renames itself at boot.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
home=${PUNCHCLOCK_HOME:-$HOME/.local/share/punchclock}
ctx=$(mktemp -d)
trap 'rm -rf "$ctx"' EXIT
cp -R "$here/Containerfile" "$here/units" "$here/claude-settings.json" "$here/punchclock-identity" "$ctx/"
cp "${PUNCHCLOCK:-$HOME/.local/bin/punchclock}" "$ctx/punchclock"
cp "${FERRY:-$HOME/.local/bin/ferry}" "$ctx/ferry"
cp /opt/hallpass/bin/hallpass "$ctx/hallpass"
mkdir "$ctx/hallpass-etc"
cp -R /etc/hallpass/gitconfig /etc/hallpass/allowed_signers /etc/hallpass/signing.pub /etc/hallpass/gh "$ctx/hallpass-etc/"
sed -i 's|^http_unix_socket:.*|http_unix_socket: /run/hallpass/gh.sock|' "$ctx/hallpass-etc/gh/config.yml"

podman build --pull=newer --build-arg USER="$(id -un)" --build-arg UID="$(id -u)" -t localhost/punchclock-box "$ctx"
podman rm -f punchclock-root >/dev/null 2>&1 || true
podman create --name punchclock-root localhost/punchclock-box >/dev/null
mkdir -p "$home/image"
out="$home/image/rootfs.ext4.new"
podman unshare sh -c '
  m=$(podman mount punchclock-root)
  printf "127.0.0.1 localhost\n127.0.1.1 box\n::1 localhost ip6-localhost\n" > $m/etc/hosts
  echo box > $m/etc/hostname
  truncate -s 12G "$1" && mkfs.ext4 -q -F -L root -d $m "$1"
  podman unmount punchclock-root >/dev/null' sh "$out"
podman rm punchclock-root >/dev/null
mv "$out" "$home/image/rootfs.ext4"
echo "rootfs.ext4: $(du -h "$home/image/rootfs.ext4" | cut -f1) used"
