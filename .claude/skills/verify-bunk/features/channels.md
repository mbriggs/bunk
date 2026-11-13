# What a box can reach

A box has no network device. It reaches an HTTPS proxy that allows only allowlisted names, the host's hallpass broker for git and gh against GitHub, and the host's clock. `check.sh` checks every channel in a running box, one line each.

## Sub-features

- `chan-check` `check.sh BOX` prints `ok` for each of its nine checks and exits 0.
- `chan-offline` `OFFLINE=1` prints `skip` for the two hallpass checks and the Claude check.
- `chan-refuse` a name off the allowlist is refused, and `supervisor.log` says so.
- `chan-github` GitHub's names are refused by the proxy even in the allowlist; they go through hallpass.
- `chan-edit` an edit to `allow.txt` applies to the next request, with no restart.
- `chan-open` mode `open` allows any name but GitHub's.

## How to get to it (user POV)

- `./check.sh BOX` from the checkout, after `bunk up BOX`.
- Any command in a box that makes a request: `bunk ssh BOX curl ...`, or under `bunk run`.
- Editing `$BUNK_HOME/allow.txt` and `$BUNK_HOME/mode`.

## Driving it with bunkv

Preconditions:

- The baseline, with `$v do up bunk up c1` done. `H=$BUNKV/home`, and the shell is in the checkout.
- `$v doctor` notes a Claude token and a hallpass broker. Without both, only the `OFFLINE=1` bullet can run, and `chan-check` is reported unverified.

- **Every channel.** Run `$v do chan-check ./check.sh c1`. Nine lines, each starting `ok`, and exit 0. It takes about 15 seconds; Claude makes one small request.
- **Offline.** Run `$v do chan-offline env OFFLINE=1 ./check.sh c1`. Six `ok` lines, three `skip` lines, exit 0.
- **Refused.** Run `$v do chan-refuse bunk ssh c1 'curl -s -o /dev/null -w "%{http_code}\n" https://example.com/'`, then `$v do chan-refuse-log grep example.com "$H/boxes/c1/supervisor.log"`. The code is `000`, and the log has `egress: REFUSED example.com: not allowlisted`.
- **Edit the allowlist.** Run `echo example.com >> "$H/allow.txt"`, then the same `curl` as `chan-edit`. The code is `200`, and the log gains `egress: allowed example.com:443`.
- **GitHub stays refused.** Run `echo github.com >> "$H/allow.txt"`, then `$v do chan-github bunk ssh c1 'curl -s -o /dev/null -w "%{http_code}\n" https://github.com/'`. The code is `000`, and the log has `REFUSED github.com: GitHub goes through hallpass only`.
- **Open mode.** Run `echo open > "$H/mode"`, then `curl` `https://example.org/` and `https://github.com/` the same way as `chan-open`. `200` and `000`. Run `echo allowlist > "$H/mode"`.
- **Leave it clean.** Run `cp allow.txt "$H/allow.txt"` and `$v do rm bunk rm c1`.

## Gotchas

- `check.sh` reads `BUNK` for the binary. `bunkv do` sets it; a bare `./check.sh` would use the installed bunk and the user's real boxes.
- The hallpass checks clone `mbriggs/hallpass` and call `gh api` through the user's real broker. Both only read.
- The clock check compares the box's clock with a `Date` header from `registry.npmjs.org`, so it fails when egress does.
- Run `check.sh` before the allowlist bullets. With `example.com` allowed, its `egress refuses example.com` line is a `FAIL`.
- The edits are to the run's `allow.txt`. Leave `~/.local/share/bunk/allow.txt` alone.
- `000` is curl's code for a connection the proxy refused.
