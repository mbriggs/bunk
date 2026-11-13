# Box lifecycle

A box can be made by name, outside any directory. `bunk up NAME` makes and boots it, `bunk ls` lists boxes, `bunk ssh` logs in, `bunk rm NAME` deletes it, and `bunk gc` deletes stopped boxes nobody has used. A limit caps how many run at once.

## Sub-features

- `box-up` makes and boots a box, prints `NAME: up in ...` on stderr and its SSH host key on stdout.
- `box-up-again` on a running box converges: same key, no second VM.
- `box-ls` lists each box as name, `running` or `stopped`, and when it was last used, then `N of MAX running`.
- `box-ssh` runs a command in the box as the host's user, with the host's home path.
- `box-rm` stops and deletes the box and its directory; a missing box is an error.
- `box-name` refuses a name that isn't 1 to 32 lowercase letters, digits, and dashes, starting with a letter.
- `box-limit` refuses to start a box past the limit in `max-running`.
- `box-gc` deletes stopped boxes unused for the given days, and never a running one.

## How to get to it (user POV)

- `bunk up NAME`, `bunk ls`, `bunk ssh NAME [CMD...]`, `bunk rm NAME`, `bunk gc [DAYS]`.
- The limit: a number in `$BUNK_HOME/max-running`.

## Driving it with bunkv

Preconditions:

- The baseline. `H=$BUNKV/home`.

- **Up.** Run `$v do box-up bunk up c1`. Exit 0, with `c1: up in` and a line starting `ssh-ed25519 `.
- **Up again.** Run `$v do box-up-again bunk up c1`. The same key. `$v doctor` shows one supervisor for `c1`.
- **List.** Run `$v do box-ls bunk ls`. `c1	running	used 0s ago` and `1 of 8 running`.
- **ssh.** Run `$v do box-ssh bunk ssh c1 'hostname; whoami; echo $HOME'`. It prints `c1`, the host's user name, and the host's home path.
- **Bad name.** Run `$v do box-name bunk up Bad_Name`. Exit 1 with `use 1-32 lowercase letters`, and no directory appears in `$H/boxes`.
- **Limit.** Run `echo 1 > "$H/max-running"`, then `$v do box-limit bunk up c2`. Exit 1 with `the limit on running boxes (1) is reached`, and `bunk ls` shows no `c2`. Run `rm "$H/max-running"`.
- **gc leaves a running box.** Run `$v do box-gc-running bunk gc 0`. No output, and `c1` is still listed.
- **gc on a stopped box.** No command stops a box and keeps it. A box stops when its supervisor ends, as at a reboot, so end this run's own: `kill -TERM "$(cat "$H/boxes/c1/pid")"`, and wait until `bunk ls` shows `c1	stopped` (about 5 seconds). Run `$v do box-gc-7 bunk gc`: no output, `c1` still listed. Run `$v do box-gc-0 bunk gc 0`: `removed c1`, and `bunk ls` shows `0 of 8 running`.
- **gc's argument.** Run `$v do box-gc-bad bunk gc x`. Exit 1 with `isn't a number of days`.
- **Remove.** Run `$v do box-up2 bunk up c1`, `$v do box-rm bunk rm c1`, then `$v do box-rm-again bunk rm c1`. The first `rm` exits 0 and `$H/boxes/c1` is gone; the second exits 1 with `no box c1`.

## Gotchas

- The `kill` goes only to the pid in a box directory under `$BUNKV/home`, after `$v doctor` has said that supervisor is this run's.
- `bunk ls` writes the count line to stderr. `bunkv do` records both streams.
- `bunk gc` also deletes a stopped box whose directory is gone, at any age. To drive that, make the box with `bunk run` in a `bunkv repo` directory, stop it as above, delete the directory, and run `bunk gc`.
- `bunk ssh NAME` with no command opens a login shell, which needs a terminal: use a Rex pane.
- An unknown command prints the usage and exits 2.
