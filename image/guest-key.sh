#!/bin/sh
# guest-key.sh: print a hash of the working tree's files that the box image is
# built from: image/ and the guest's side of bunk. build.sh records it beside
# the image, and bin/verify rebuilds a lane's image when it no longer matches.
# Outside a git checkout, such as a copy of image/ on a build machine, it
# prints nothing.
set -eu
cd "$(dirname "$0")/.."
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0
paths='image relay clock main.go go.mod go.sum'
# shellcheck disable=SC2086 # paths is a list of pathspecs
{
  git ls-files -- $paths
  git ls-files -z -- $paths | xargs -0 git hash-object
} | git hash-object --stdin
