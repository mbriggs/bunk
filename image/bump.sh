#!/bin/sh
# bump.sh: pin the box image's agents to this machine's Claude and Codex, and
# Rex to the newest release on its channel, by rewriting ./versions. Run it on
# the laptop: ferry won't resume a session in a box whose Claude is older
# than the laptop's. Then commit versions and rebuild the image on each
# build machine.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
channel=${REX_CHANNEL:-preview}
claude=$(claude --version | cut -d' ' -f1)
codex=$(codex --version | awk '{print $NF}' | tail -1)
release=$(curl -fsSL "https://blob.superlogical.com/api/releases/rex/$channel")
rex=$(printf '%s' "$release" | python3 -I -c '
import json, sys
r = json.load(sys.stdin)
print("REX_VERSION=" + r["version"])
print("REX_COMMIT=" + r["commit"])
for a in r["artifacts"]:
    if a["os"] == "linux" and a["filename"] == "rex-linux-%s.tar.gz" % a["arch"]:
        print("REX_SHA256_%s=%s" % (a["arch"], a["sha256"]))
')
cat > "$here/versions" <<VERSIONS
# The agents baked into the box image. image/bump.sh rewrites this file;
# build.sh installs exactly these.
CLAUDE=$claude
CODEX=$codex
REX_CHANNEL=$channel
$rex
VERSIONS
cat "$here/versions"
