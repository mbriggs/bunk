#!/bin/sh
# fetch-firecracker.sh: install the pinned Firecracker and guest kernel into
# $BUNK_HOME on a Linux x86_64 build machine, checking both against their
# sha256. The kernel is from Firecracker's CI artifacts: it has vsock built in
# and boots without an initrd.
set -eu
home=${BUNK_HOME:-$HOME/.local/share/bunk}
fc=v1.17.0
fc_sha=06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558
kernel=https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.15/x86_64/vmlinux-6.1.155
kernel_sha=e20e46d0c36c55c0d1014eb20576171b3f3d922260d9f792017aeff53af3d4f2

[ "$(uname -sm)" = "Linux x86_64" ] || { echo "fetch-firecracker.sh: for Linux x86_64 only" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
check() { [ "$(sha256sum "$1" | cut -d' ' -f1)" = "$2" ] || { echo "fetch-firecracker.sh: $1 doesn't match its pinned sha256" >&2; exit 1; }; }
curl -fsSL -o "$tmp/fc.tgz" "https://github.com/firecracker-microvm/firecracker/releases/download/$fc/firecracker-$fc-x86_64.tgz"
check "$tmp/fc.tgz" "$fc_sha"
curl -fsSL -o "$tmp/vmlinux" "$kernel"
check "$tmp/vmlinux" "$kernel_sha"
tar -xzf "$tmp/fc.tgz" -C "$tmp"
mkdir -p "$home/bin" "$home/image"
install -m 755 "$tmp/release-$fc-x86_64/firecracker-$fc-x86_64" "$home/bin/firecracker"
install -m 644 "$tmp/vmlinux" "$home/image/vmlinux"
echo "installed Firecracker $fc and $(basename "$kernel") in $home"
