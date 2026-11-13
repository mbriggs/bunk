# bunk verification map

The maintained source for verifying what a user of bunk sees. Read this index, then use the matching feature file as the recipe. [SKILL.md](../SKILL.md) covers launch, doctor, evidence, and cleanup.

## Baseline

- A run directory from `bunkv launch`, with `BUNKV` exported and `v` set to the path of `bunkv`.
- `$v doctor` exits 0.
- Every recipe starts with no boxes in the run's home (`$v do ls bunk ls` prints only `0 of 8 running`), unless its preconditions say otherwise. Remove a recipe's boxes when it ends.
- A recipe that needs a worktree makes its own with `$v repo NAME` and runs from inside it. One directory has one box, named for the directory and a hash of its path (`demo-87ccaf`); `bunk ls` shows the name.

## Conventions

- Each bullet in a `Driving it` section pairs a user action with the command and the result that must be observed. Run commands as written, each under `$v do LABEL`.
- Commits inside a box pass `-c user.name=box -c user.email=box@example.invalid`, so a proof doesn't depend on the image's git identity. Written `$gitbox` in the recipes: `gitbox='git -c user.name=box -c user.email=box@example.invalid'`, defined inside the `sh -c` string.
- An entry point that can't be reached is reported with the command tried and what was missing. A path proved through one entry point isn't reported as proved through another.
- Not mapped: `bunk codex-login` (needs a person and writes the real Keychain), `bunk proxy`, `bunk supervise`, and `bunk guest` (internals), and the ferry integration (needs ferry and a second machine).

## Feature file shape

An H1, one paragraph on what the user sees, then four H2s in this order: `Sub-features` (short IDs), `How to get to it (user POV)` (every entry point), `Driving it with bunkv` (starts with `Preconditions:`), and `Gotchas`.

## Features

- [Run a command in a directory's box](./run.md): copy in, run, copy back, exit status, box reuse.
- [Work that didn't come back](./held-work.md): a moved branch, the held box, `bunk rm --dir`.
- [Box lifecycle](./boxes.md): `up`, `ls`, `ssh`, `rm`, `gc`, and the running limit.
- [What a box can reach](./channels.md): `check.sh`, the egress allowlist, hallpass, the clock.
- [Agents in a box](./agents.md): `bunk run claude`, config in, sessions back, the mirrored session, SIGTERM.
