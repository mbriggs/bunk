#!/bin/sh
# check.sh BOX: check a running box end to end, on its build machine. Each
# line is one channel or promise; the exit status is the number that failed.
# Run it after changing the image, the kernel, or bunk.
#
#   bunk up c1 && ./check.sh c1 && bunk rm c1
#
# With OFFLINE=1, it skips the checks that need a Claude token or the build
# machine's hallpass broker, as in CI.
set -u
"${BUNK:-bunk}" ssh "$1" "OFFLINE=${OFFLINE:-} sh -s" <<'CHECKS'
fails=0
online() { if [ -n "$OFFLINE" ]; then echo "skip  $1"; else check "$@"; fi; }
check() { name=$1; shift; if out=$("$@" 2>&1); then echo "ok    $name"; else echo "FAIL  $name: $(echo "$out" | tail -1)"; fails=$((fails+1)); fi; }
check "hostname is the box's name"  sh -c '[ "$(hostname)" != box ]'
check "clock within 5s of registry.npmjs.org's" sh -c 'd=$(curl -sI https://registry.npmjs.org/ | tr -d "\r" | sed -n "s/^[Dd]ate: //p"); [ -n "$d" ] && t=$(date -d "$d" +%s) && n=$(date +%s) && [ $((n-t)) -lt 5 ] && [ $((t-n)) -lt 5 ]'
check "egress allows registry.npmjs.org" sh -c '[ "$(curl -s -o /dev/null -w %{http_code} https://registry.npmjs.org/)" = 200 ]'
check "egress refuses example.com"      sh -c '! curl -sf -o /dev/null https://example.com/'
check "egress refuses github.com"       sh -c '! curl -sf -o /dev/null https://github.com/'
check "apt through the proxy"           sudo apt-get update -qq
online "hallpass: git clone"             sh -c 'rm -rf /tmp/c-hp && git clone -q --depth 1 git@github.com:mbriggs/hallpass.git /tmp/c-hp'
online "hallpass: gh api"                /opt/hallpass/bin/hallpass gh api repos/mbriggs/hallpass --jq .full_name
online "claude edits without a prompt, commits signed" sh -c '
  rm -rf /tmp/c-t && mkdir /tmp/c-t && cd /tmp/c-t && git init -q && echo a > f && git add f && git commit -qm a &&
  claude -p "Append the line b to the file f and commit it with the message b." >/dev/null &&
  grep -qx b f && [ "$(git log -1 --format=%G?)" = G ]'
exit $fails
CHECKS
