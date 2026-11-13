# Work that didn't come back

When the host's branch moves while the box has it, bunk refuses to move it again. Nothing comes back but Claude's sessions, bunk names the box's commit, which is already in the host's repository, and the box is held: the next `bunk run` refuses to copy over it until the user deletes the box.

## Sub-features

- `held-refused` the run exits 1 and names the branch and the box's commit; the host's branch and files are as the host left them.
- `held-commit` the named commit is in the host's repository, unreferenced.
- `held-run` the next `bunk run` in the directory exits 1 and says how to reach the box.
- `held-rm` `bunk rm --dir DIR` refuses a held box, and one a run is using.
- `held-force` `bunk rm --dir DIR --force` deletes it.
- `held-none` `bunk rm --dir` on a directory with no box does nothing and exits 0.

## How to get to it (user POV)

- Commit on the host's branch while `bunk run` is going in that worktree.
- `bunk rm --dir DIR` and `bunk rm --dir DIR --force`.

## Driving it with bunkv

Preconditions:

- The baseline. `r=$($v repo held)`, and the shell is in `$r`.

- **Move the branch under a run.** Start `$v do held-run bunk run sh -c 'echo two > two && git add two && git -c user.name=box -c user.email=box@example.invalid commit -qm box-two && sleep 8' &`. After 3 seconds run `git -c user.name=h -c user.email=h@example.invalid -c commit.gpgsign=false commit -q --allow-empty -m host-moved`, then `wait`. The run's evidence file says `refs/heads/main moved here while the box had it`, gives a 12-digit commit, and ends `# exit: 1`.
- **Host state.** Run `$v do held-host sh -c 'git log --oneline; ls; git log --oneline -1 COMMIT'` with the commit from the message. The branch's top is `host-moved`, there is no file `two`, and the last line is `box-two`.
- **The next run refuses.** Run `$v do held-next bunk run true`. Exit 1, with `holds work that didn't come back` and an `ssh -F ... ssh_config` command for the box.
- **The box still has the work.** Run `$v do held-in-box bunk ssh BOX cat "$r/two"` with the box's name from `bunk ls`. It prints `two`.
- **rm refuses.** Run `$v do held-rm bunk rm --dir "$r"`. Exit 1, ending `(bunk rm --dir --force removes it anyway)`. `bunk ls` still shows the box.
- **Force removes.** Run `$v do held-force bunk rm --dir "$r" --force`, then `$v do held-ls bunk ls`. Exit 0, and `0 of 8 running`.
- **No box.** Run `$v do held-none bunk rm --dir "$r"` again. Exit 0, no output.
- **rm refuses a box in use.** Start `$v do held-busy-a bunk run sleep 8 &`, and after 3 seconds run `$v do held-busy-b bunk rm --dir "$r"`. Exit 1 with `another bunk run is using box` and `it's still running`. After the run ends, `bunk rm --dir "$r"` exits 0.

## Gotchas

- The host commit has to land after the run's copy in and before its command ends. A 3-second wait inside an 8-second command leaves room for a cold boot.
- A shell's bare `wait` returns 0. The run's status is in its evidence file.
- The box's commit doesn't show in `git log --all`: no ref names it. Look it up by the hash in the message.
- `bunk rm NAME`, by name, deletes a held box without asking. Only `--dir` refuses. That's how `bunkv cleanup` works, and it isn't a bug.
