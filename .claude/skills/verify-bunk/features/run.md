# Run a command in a directory's box

`bunk run CMD...` copies the current directory into its own box, runs CMD there at the same path, and copies the results back: the working tree, new git objects, and the branch. bunk exits with CMD's status.

## Sub-features

- `run-git` runs in a git worktree; files and a commit made in the box arrive on the host.
- `run-status` exits with the command's own status.
- `run-plain` runs in a directory outside git; created and deleted files both come back.
- `run-narrow` brings back nothing else from `.git`: no hooks, no config.
- `run-reuse` reuses the box, so what the last run left outside the worktree is still there.
- `run-busy` refuses a second run on the same directory while one is going.

## How to get to it (user POV)

- `bunk run CMD...` in a git worktree.
- `bunk run CMD...` in a subdirectory of one; the command runs in that subdirectory.
- `bunk run CMD...` in a directory outside git.

## Driving it with bunkv

Preconditions:

- The baseline. `r=$($v repo demo)`, and the shell is in `$r`.

- **Worktree entry, commit, status.** Run `$v do run-git bunk run sh -c 'hostname; pwd; echo from-box > made-in-box; git add made-in-box && git -c user.name=box -c user.email=box@example.invalid commit -qm "box commit"; exit 7'`. The output has the box's name (`demo-` and six hex digits) and `$r`, and the exit status is 7.
- **Host state.** Run `$v do run-git-host sh -c 'cat made-in-box; git log --oneline; git status --short; bunk ls'`. It prints `from-box`, the log has `box commit` above `seed`, the status is empty, and `bunk ls` shows the box `running`.
- **Hooks and config stay out.** Run `$v do run-narrow bunk run sh -c 'printf "#!/bin/sh\n" > .git/hooks/pre-commit; chmod +x .git/hooks/pre-commit; git config core.evil yes'`, then `$v do run-narrow-host sh -c 'ls .git/hooks/pre-commit; git config core.evil; true'`. `ls` reports no such file, and `git config` prints nothing.
- **Subdirectory entry.** Run `mkdir -p sub && cd sub && $v do run-sub bunk run sh -c 'pwd; touch here'; cd ..`. The output is `$r/sub`, `sub/here` exists on the host, and `bunk ls` still shows one box.
- **Reuse.** Run `$v do run-reuse-1 bunk run sh -c 'touch ~/kept-in-box'`, then `$v do run-reuse-2 bunk run sh -c 'ls ~/kept-in-box'`. The second prints the path and exits 0.
- **Busy.** Start `$v do run-busy-a bunk run sleep 8 &`, wait 3 seconds, and run `$v do run-busy-b bunk run true`. It exits 1 with `another bunk run is using box`. Let the first finish, and read its `# exit: 0` from its evidence file.
- **Plain directory entry.** Run `mkdir "$BUNKV/w/plain" && cd "$BUNKV/w/plain" && echo a > a`, then `$v do run-plain bunk run sh -c 'echo b > b; rm a; mkdir sub; echo c > sub/c'`, then `$v do run-plain-host find . -type f`. It lists `./b` and `./sub/c`, and not `./a`.
- **Leave it clean.** Run `$v do run-rm bunk rm --dir "$r"` and the same for `$BUNKV/w/plain`. `bunk ls` shows neither box.

## Gotchas

- `bunkv do` has no terminal, so ssh runs the command without one. Anything that draws a screen goes through a Rex pane; see [agents](./agents.md).
- The first run in a directory boots its box, about 2 seconds on a Mac. A later run takes well under one.
- A commit in a box is signed through the host's hallpass broker. `hallpass: reading broker response: ... i/o timeout` followed by `failed to write commit object` is the broker, not the copy back. It happened once in ten commits while this map was written. Report it, and drive the bullet again in a new `bunkv repo`.
- Git in the box warns `untracked cache is disabled on this system or location`. It's noise.
- The copy back deletes host files the box deleted. Keep runs inside `$BUNKV/w`.
- `bunk run` with no command prints the usage and exits 2.
