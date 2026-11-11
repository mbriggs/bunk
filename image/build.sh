#!/bin/sh
# build.sh: build the box image from scratch and install it in
# $PUNCHCLOCK_HOME/image: rootfs.ext4, plus on a Mac the kernel (Image) and
# the empty initrd vfkit requires.
#
# The context is this directory plus what boxes take from the build machine:
# punchclock and ferry, hallpass's client binary and config, since boxes share
# its broker, and the git user to commit as. On Linux the binaries are the
# ones this machine runs (ferry push keeps ferry in step with the laptop). A
# Mac has no Linux binaries of its own, so punchclock and hallpass are
# cross-built from their checkouts, and ferry comes from its dist/. PUNCHCLOCK,
# FERRY, and HALLPASS override any of them.
#
# Claude, Codex, and Rex reinstall at their latest versions on the first build
# of each day (AGENTS=now forces it). The filesystem is made in a container
# from the image itself, since macOS has no mkfs.ext4.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
home=${PUNCHCLOCK_HOME:-$HOME/.local/share/punchclock}
work="$home/image/build"
rm -rf "$work"
mkdir -p "$work/ctx"
trap 'rm -rf "$work"' EXIT
ctx="$work/ctx"

case $(uname -s) in
Linux)
  C=podman PULL=--pull=newer
  cp "${PUNCHCLOCK:-$HOME/.local/bin/punchclock}" "$ctx/punchclock"
  cp "${FERRY:-$HOME/.local/bin/ferry}" "$ctx/ferry"
  cp "${HALLPASS:-/opt/hallpass/bin/hallpass}" "$ctx/hallpass"
  ;;
Darwin)
  C=docker PULL=--pull
  if [ -n "${PUNCHCLOCK:-}" ]; then cp "$PUNCHCLOCK" "$ctx/punchclock"
  else (cd "$here/.." && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$ctx/punchclock" .); fi
  if [ -n "${HALLPASS:-}" ]; then cp "$HALLPASS" "$ctx/hallpass"
  else (cd "$HOME/src/hallpass" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$ctx/hallpass" ./cmd/hallpass); fi
  cp "${FERRY:-$HOME/src/ferry/dist/ferry-linux-arm64}" "$ctx/ferry"
  ;;
*)
  echo "build.sh: no box backend for $(uname -s)" >&2
  exit 1
  ;;
esac

cp -R "$here/Containerfile" "$here/units" "$here/claude-settings.json" "$here/punchclock-identity" "$ctx/"
mkdir "$ctx/hallpass-etc"
cp -R /etc/hallpass/gitconfig /etc/hallpass/allowed_signers /etc/hallpass/signing.pub /etc/hallpass/gh "$ctx/hallpass-etc/"
# In a box, hallpass's sockets are always in /run/hallpass.
sed 's|^http_unix_socket:.*|http_unix_socket: /run/hallpass/gh.sock|' /etc/hallpass/gh/config.yml > "$ctx/hallpass-etc/gh/config.yml"
git config -f "$ctx/user.gitconfig" user.name "$(git config --global user.name)"
git config -f "$ctx/user.gitconfig" user.email "$(git config --global user.email)"

$C build -q $PULL -t localhost/punchclock-box -f "$ctx/Containerfile" \
  --build-arg USER="$(id -un)" --build-arg UID="$(id -u)" \
  --build-arg AGENTS="${AGENTS:-$(date +%F)}" "$ctx" >/dev/null

$C rm -f punchclock-root >/dev/null 2>&1 || true
$C create --name punchclock-root localhost/punchclock-box >/dev/null
$C export punchclock-root > "$work/rootfs.tar"
$C rm punchclock-root >/dev/null
# podman and docker own /etc/hosts, /etc/hostname, and /etc/resolv.conf in a
# container, so they're written here. Each box renames itself at boot, and
# has no resolver: it reaches names only through the proxy.
# The tree is unpacked inside the container, not in the shared $work: a Mac
# folder mounted into Docker doesn't keep Linux file owners, and a home
# directory owned by root makes sshd refuse every login.
$C run --rm -v "$work:/w" localhost/punchclock-box sh -euc '
  mkdir /r
  tar -x -p -C /r -f /w/rootfs.tar
  rm /w/rootfs.tar
  printf "127.0.0.1 localhost\n127.0.1.1 box\n::1 localhost ip6-localhost\n" > /r/etc/hosts
  echo box > /r/etc/hostname
  rm -f /r/etc/resolv.conf && : > /r/etc/resolv.conf
  for k in /r/boot/vmlinuz-*; do [ -f "$k" ] && cp "$k" /w/vmlinuz; done
  truncate -s 12G /w/rootfs.ext4
  mkfs.ext4 -q -F -L root -d /r /w/rootfs.ext4'

if [ "$(uname -s)" = Darwin ]; then
  gunzip -c "$work/vmlinuz" > "$home/image/Image"
  : | cpio -o -H newc --quiet > "$home/image/initrd.cpio"
fi
mv "$work/rootfs.ext4" "$home/image/rootfs.ext4"
echo "rootfs.ext4: $(du -h "$home/image/rootfs.ext4" | cut -f1) used"
