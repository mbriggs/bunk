#!/usr/bin/env bash
# Model-checks bunk's copy-back contract with TLC, and compares the rules each
# scenario breaks with the ones it's known to break. See
# .claude/skills/verify-copy-back/SKILL.md.
#
#   verify/copy-back/check.sh           run every scenario and the drift check
#   verify/copy-back/check.sh --resync  record the covered files' hashes (only
#                                       after you've confirmed the model
#                                       describes them)
#
# Exit 0: every scenario broke exactly its known rules, and the covered code
# hasn't changed since the last sync. Exit 1: a scenario broke a rule it isn't
# known to break, or kept one it's known to break, or TLC failed. Exit 3:
# results as expected, but covered code changed since the last sync.
# TLC's output goes to verify/copy-back/out/: SCENARIO.txt, and
# SCENARIO-RULE.txt for the shortest path to each rule that broke.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)

# Files the model describes. Keep in step with the skill's coverage table.
covered=(task/task.go box/box.go box/run.go main.go)

sum() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

if [[ "${1:-}" == "--resync" ]]; then
  (cd "$root" && sum "${covered[@]}") > "$here/covered.sha256"
  echo "recorded hashes of ${#covered[@]} covered files in verify/copy-back/covered.sha256"
  exit 0
fi

# TLC from tla2tools 1.7.4 (TLC 2.19), pinned by hash. Set TLA2TOOLS_JAR to
# use a copy you already have; it must have the same hash.
version=1.7.4
sha=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88
jar=${TLA2TOOLS_JAR:-${XDG_CACHE_HOME:-$HOME/.cache}/bunk/tla2tools-$version.jar}
if [[ ! -f "$jar" ]]; then
  mkdir -p "$(dirname "$jar")"
  echo "downloading tla2tools $version to $jar"
  curl -fsSL -o "$jar.tmp" "https://github.com/tlaplus/tlaplus/releases/download/v$version/tla2tools.jar"
  mv "$jar.tmp" "$jar"
fi
if [[ "$(sum "$jar" | cut -d' ' -f1)" != "$sha" ]]; then
  echo "$jar isn't tla2tools $version (sha256 mismatch)" >&2
  exit 1
fi
command -v java >/dev/null || { echo "java not found; TLC needs Java 11 or later" >&2; exit 1; }

# scenario:the rules it's known to break. Base and KilledRun must break none.
# The others each add one thing that can go wrong, and the rules they break
# are findings about bunk: see the skill.
expect=(
  "Base:"
  "KilledRun:"
  "VMStops:RunNeverRemovesHeldWork"
  "GcRaces:GcNeverRemovesHeldWork RmDirNeverRemovesHeldWork RunNeverCopiesOverHeldWork"
)

rm -rf "$here/out"
mkdir -p "$here/out"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp "$here/CopyBack.tla" "$work/"

status=0
for e in "${expect[@]}"; do
  scenario=${e%%:*} want=${e#*:}
  cp "$here/$scenario.cfg" "$work/$scenario.cfg"
  broke=() states="?" failed=""
  # TLC stops at the first rule that breaks. Drop that rule and run again,
  # until the rest hold, to get every rule the scenario breaks.
  while :; do
    out="$work/out.txt"
    set +e
    (cd "$work" && java -XX:+UseParallelGC -jar "$jar" -config "$scenario.cfg" \
      -metadir "$work/meta" -workers 1 -cleanup CopyBack.tla) > "$out" 2>&1
    code=$?
    set -e
    rm -rf "$work/meta"
    rule=$(sed -n 's/^Error: Invariant \([A-Za-z]*\) is violated\.$/\1/p' "$out" | head -1)
    if [[ $code -eq 0 ]]; then
      states=$(sed -n 's/.* \([0-9]*\) distinct states found.*/\1/p' "$out" | tail -1)
      cp "$out" "$here/out/$scenario.txt"
      break
    elif [[ -n "$rule" ]]; then
      broke+=("$rule")
      cp "$out" "$here/out/$scenario-$rule.txt"
      grep -v "^  $rule\$" "$work/$scenario.cfg" > "$work/next.cfg"
      mv "$work/next.cfg" "$work/$scenario.cfg"
    else
      cp "$out" "$here/out/$scenario.txt"
      failed="TLC error (exit $code)"
      break
    fi
  done
  got=$(printf '%s\n' ${broke[@]+"${broke[@]}"} | sort | xargs)
  want=$(printf '%s\n' $want | sort | xargs)
  if [[ -n "$failed" ]]; then
    echo "FAIL  $scenario: $failed. Output: verify/copy-back/out/$scenario.txt"
    status=1
  elif [[ "$got" == "$want" ]]; then
    echo "ok    $scenario: ${got:+breaks }${got:-holds every rule}${got:+, as known} ($states distinct states)"
  else
    echo "FAIL  $scenario: expected ${want:-no rule} to break, got ${got:-none}. Paths: verify/copy-back/out/$scenario-RULE.txt"
    status=1
  fi
done

if ! drift=$(cd "$root" && sum "${covered[@]}" | diff "$here/covered.sha256" - 2>&1); then
  echo "STALE covered code changed since the model was last confirmed against it:"
  echo "$drift" | sed -n 's/^> [0-9a-f]*  /      /p'
  echo "      Check the model against the change, then run check.sh --resync."
  [[ $status -eq 0 ]] && status=3
fi
exit $status
