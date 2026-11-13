#!/bin/sh
# build.sh: build the box image from scratch and install it in
# $BUNK_HOME/image: rootfs.ext4, plus on a Mac the kernel (Image) and
# the empty initrd vfkit requires.
#
# The context is this directory plus what boxes take from the build machine:
# bunk, hallpass's client binary and config, since boxes share its broker,
# and the git user to commit as. On Linux the binaries are the ones this
# machine runs. A Mac has no Linux binaries of its own, so bunk and hallpass
# are cross-built from their checkouts. BUNK and HALLPASS override either,
# and HALLPASS_ETC overrides /etc/hallpass, hallpass's client config.
#
# Claude, Codex, and Rex install at the versions pinned in ./versions, which
# image/bump.sh updates. The filesystem is made in a container from the image
# itself, since macOS has no mkfs.ext4.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
. "$here/versions"
home=${BUNK_HOME:-$HOME/.local/share/bunk}
work="$home/image/build"
rm -rf "$work"
mkdir -p "$work/ctx"
trap 'rm -rf "$work"' EXIT
ctx="$work/ctx"

case $(uname -s) in
Linux)
  C=podman PULL=--pull=newer
  cp "${BUNK:-$HOME/.local/bin/bunk}" "$ctx/bunk"
  cp "${HALLPASS:-/opt/hallpass/bin/hallpass}" "$ctx/hallpass"
  ;;
Darwin)
  C=docker PULL=--pull
  if [ -n "${BUNK:-}" ]; then cp "$BUNK" "$ctx/bunk"
  else (cd "$here/.." && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$ctx/bunk" .); fi
  if [ -n "${HALLPASS:-}" ]; then cp "$HALLPASS" "$ctx/hallpass"
  else (cd "$HOME/src/hallpass" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$ctx/hallpass" ./cmd/hallpass); fi
  ;;
*)
  echo "build.sh: no box backend for $(uname -s)" >&2
  exit 1
  ;;
esac

case $(uname -m) in x86_64 | amd64) arch=amd64 ;; *) arch=arm64 ;; esac
eval "rex_sha=\$REX_SHA256_$arch"
curl -fsSL -o "$work/rex.tar.gz" "https://blob.superlogical.com/download/rex/$REX_CHANNEL/$REX_COMMIT/rex-linux-$arch.tar.gz"
got=$(if command -v sha256sum >/dev/null; then sha256sum; else shasum -a 256; fi < "$work/rex.tar.gz" | cut -d' ' -f1)
[ "$got" = "$rex_sha" ] || { echo "build.sh: rex-linux-$arch.tar.gz is $got, not the pinned $rex_sha" >&2; exit 1; }
tar -xzf "$work/rex.tar.gz" -C "$ctx" rex
cp -R "$here/Containerfile" "$here/units" "$here/managed-settings.json" "$here/codex-managed-config.toml" "$here/bunk-identity" "$ctx/"
mkdir "$ctx/hallpass-etc"
hp=${HALLPASS_ETC:-/etc/hallpass}
cp -R "$hp/gitconfig" "$hp/allowed_signers" "$hp/signing.pub" "$hp/gh" "$ctx/hallpass-etc/"
# In a box, hallpass's sockets are always in /run/hallpass.
sed 's|^http_unix_socket:.*|http_unix_socket: /run/hallpass/gh.sock|' "$hp/gh/config.yml" > "$ctx/hallpass-etc/gh/config.yml"
git config -f "$ctx/user.gitconfig" user.name "$(git config --global user.name)"
git config -f "$ctx/user.gitconfig" user.email "$(git config --global user.email)"

$C build -q $PULL -t localhost/bunk-box -f "$ctx/Containerfile" \
  --build-arg USER="$(id -un)" --build-arg UID="$(id -u)" --build-arg HOMEDIR="$HOME" \
  --build-arg CLAUDE="$CLAUDE" --build-arg CODEX="$CODEX" "$ctx" >/dev/null

$C rm -f bunk-root >/dev/null 2>&1 || true
$C create --name bunk-root localhost/bunk-box >/dev/null
$C export bunk-root > "$work/rootfs.tar"
$C rm bunk-root >/dev/null
# podman and docker own /etc/hosts, /etc/hostname, and /etc/resolv.conf in a
# container, so they're written here. Each box renames itself at boot, and
# has no resolver: it reaches names only through the proxy.
# The tree is unpacked inside the container, not in the shared $work: a Mac
# folder mounted into Docker doesn't keep Linux file owners, and a home
# directory owned by root makes sshd refuse every login.
$C run --rm -v "$work:/w" localhost/bunk-box sh -euc '
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
