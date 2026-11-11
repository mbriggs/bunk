#!/bin/sh
# check.sh SSH_TARGET: check a running box end to end, from the laptop.
# Each line is one channel or promise; the exit status is the number that
# failed. Run it after changing the image, the kernel, or bunk.
#
#   ./check.sh k1.kant.box
set -u
ssh -o ConnectTimeout=20 "$1" 'sh -s' <<'CHECKS'
fails=0
check() { name=$1; shift; if out=$("$@" 2>&1); then echo "ok    $name"; else echo "FAIL  $name: $(echo "$out" | tail -1)"; fails=$((fails+1)); fi; }
check "hostname is the box's name"  sh -c '[ "$(hostname)" != box ]'
check "clock within 5s of api.anthropic.com's" sh -c 'd=$(curl -sI http://127.0.0.1:8787/ | tr -d "\r" | sed -n "s/^[Dd]ate: //p"); [ -n "$d" ] && t=$(date -d "$d" +%s) && n=$(date +%s) && [ $((n-t)) -lt 5 ] && [ $((t-n)) -lt 5 ]'
check "egress allows registry.npmjs.org" sh -c '[ "$(curl -s -o /dev/null -w %{http_code} https://registry.npmjs.org/)" = 200 ]'
check "egress refuses example.com"      sh -c '! curl -sf -o /dev/null https://example.com/'
check "egress refuses github.com"       sh -c '! curl -sf -o /dev/null https://github.com/'
check "apt through the proxy"           sudo apt-get update -qq
check "model endpoint swaps the token"  sh -c '[ "$(curl -s -o /dev/null -w %{http_code} -H "Authorization: Bearer sentinel" -H "anthropic-version: 2023-06-01" http://127.0.0.1:8787/v1/models)" = 200 ]'
check "hallpass: git clone"             sh -c 'rm -rf /tmp/c-hp && git clone -q --depth 1 git@github.com:mbriggs/hallpass.git /tmp/c-hp'
check "hallpass: gh api"                /opt/hallpass/bin/hallpass gh api repos/mbriggs/hallpass --jq .full_name
check "claude edits without a prompt, commits signed" sh -c '
  rm -rf /tmp/c-t && mkdir /tmp/c-t && cd /tmp/c-t && git init -q && echo a > f && git add f && git commit -qm a &&
  claude -p "Append the line b to the file f and commit it with the message b." >/dev/null &&
  grep -qx b f && [ "$(git log -1 --format=%G?)" = G ]'
check "ferry is a sandbox"              test -f ~/.config/ferry/sandbox
exit $fails
CHECKS
