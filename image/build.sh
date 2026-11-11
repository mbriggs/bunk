#!/bin/sh
# build.sh: build the box image and export it to $PUNCHCLOCK_HOME/image/rootfs.ext4.
# Run on the build machine from this directory, with a linux punchclock binary
# at ./punchclock. podman owns /etc/hosts and /etc/hostname at build time, so
# they're written here; each box renames itself at boot.
set -eu
home=${PUNCHCLOCK_HOME:-$HOME/.local/share/punchclock}
mkdir -p "$home/image"
podman build -q -t localhost/punchclock-box:amd64 . >/dev/null
podman rm -f punchclock-root >/dev/null 2>&1 || true
podman create --name punchclock-root localhost/punchclock-box:amd64 >/dev/null
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
