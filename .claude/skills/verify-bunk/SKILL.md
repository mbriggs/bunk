---
name: verify-bunk
description: Verify bunk by driving this checkout's CLI against real boxes (VMs) in a scratch BUNK_HOME. Use to prove a change to bunk works end to end, or to reproduce a bug in a real box.
---

# Verify bunk

bunk is a CLI. Each command is short-lived, but boxes (VMs) and their supervisors outlive it. `bunkv`, beside this file, builds this checkout's bunk into a run directory with its own `BUNK_HOME`, so nothing here touches the installed bunk, its boxes, or its limit on running boxes. Two runs can go side by side.

Use the installed `bunk` and `~/.local/share/bunk` for nothing: a box there may be the user's live agent.

`bin/verify` is the landing check: tests, then `check.sh` on one box. This skill is for proving a particular behavior. In a lane (the [worktree skill](../worktree/SKILL.md)), `bunkv` still makes its own home under `/tmp` and leaves the lane's home and its two-box limit alone.

## Launch

```sh
.claude/skills/verify-bunk/bunkv launch
```

It prints the run directory, `/private/tmp/bunkv-XXXXXX` on a Mac. Pass it to every later call as `BUNKV=DIR`. Launch takes under a second: it builds `DIR/bin/bunk`, clones the installed image into `DIR/home/image`, and installs the checkout's `allow.txt` in allowlist mode. There is no server to wait for. A box boots on first use, in about 2 seconds, and `bunk up` or `bunk run` returns only once the box is ready.

The cloned image holds the guest side as it was when the image was built: the box's own `bunk guest`, units, and pinned tools. When the change touches `image/`, `relay/`, `clock/`, or the `guest` commands in `main.go`, build the image from the checkout instead. It needs Docker running and takes about a minute:

```sh
.claude/skills/verify-bunk/bunkv launch --build-image
```

After editing Go code, launch a new run. Doctor fails a run whose Go source changed after its binary was built.

## Doctor

```sh
BUNKV=DIR .claude/skills/verify-bunk/bunkv doctor
```

Read-only. Each line starts with `ok`, `FAIL`, or `note`, and the exit status is the number of failures. It checks that the binary is this checkout's at its current revision, the image is whole, the VMM is there, and each running box's supervisor is this run's binary. The notes say where the image came from, and whether a Claude token and a hallpass broker are there. Without either, run `check.sh` with `OFFLINE=1`.

## Drive

`bunkv do LABEL CMD...` runs CMD with this run's `BUNK_HOME`, with its `bunk` first on `PATH`, and with `BUNK` set for `check.sh`. It records the command, its output, and its exit status as evidence, and exits with CMD's status.

```sh
v=$PWD/.claude/skills/verify-bunk/bunkv; export BUNKV=DIR
r=$($v repo demo)                      # a scratch git repository with one commit, in DIR/w/demo
cd "$r" && $v do run bunk run sh -c 'hostname; echo hi > new && git add new && git commit -qm box'
$v do host-state sh -c 'git log --oneline; git status --short; bunk ls'
```

`bunk run` works on the directory it's run in, so drive it only from a `bunkv repo` directory, never from this checkout. Run `bunkv do` in the foreground. A shell's bare `wait` returns 0 whatever a background job did; read its status from the evidence file's `# exit:` line.

An interactive agent (`bunk run claude`, `bunk run codex`) needs a terminal, and `bunkv do` pipes its output. Drive it in a Rex tab, as a user would:

```sh
rex run -s "$REX_SESSION" --focus=false --label bunkv-agent -c "$r" --keep-open --json -- \
  env BUNK_HOME="$BUNKV/home" "$BUNKV/bin/bunk" run claude        # .block_ids[0] is the pane
rex send -b BLOCK 'the prompt' && rex send-key -b BLOCK enter
rex capture -b BLOCK --trim | grep -v '^\s*$'                      # the screen is mostly blank lines
rex block close BLOCK
```

Claude's prompt is ready when the capture shows `bypass permissions on`. Without Rex, `bunk run claude -p PROMPT` under `bunkv do` covers everything but the terminal.

The recipes for each feature are in [features/](features/README.md). Read its index before driving, and drive every entry point the feature's file lists.

## Evidence

Evidence goes in `~/.cache/bunk-verify/<run name>/`, which `bunkv evidence` prints: one numbered file per `bunkv do`, with the time, the bunk revision, the directory, the command, stdout and stderr together in the order they came, and `# exit: N`. Save pane captures there yourself (`rex capture ... > "$($v evidence)/NN-label.txt"`).

A proof holds to these standards:

- Go through `bunk`'s own commands, as a user types them. `bunk supervise`, `bunk proxy`, and `bunk guest` are bunk's internals, and editing a box's directory by hand proves nothing about bunk.
- Record the action and then the state it left, each with its own `bunkv do`: the host's worktree and `git log` after a run, `bunk ls` after an `up` or `rm`, `supervisor.log` for an egress decision.
- Check a side effect on the side it lands. Work that came back is checked on the host, not by asking the box.
- Nothing is mocked. The box is a real VM, and the Claude token and hallpass broker are the user's real ones. With `OFFLINE=1`, `check.sh` prints `skip` for hallpass and Claude, and those stay unverified: say so.

## Cleanup

```sh
BUNKV=DIR .claude/skills/verify-bunk/bunkv cleanup
```

It removes each box in the run's home with `bunk rm NAME`, which stops the supervisor recorded in the box's own directory. Then it moves Claude's session folders for the run's worktrees out of `~/.claude/projects` into the evidence, and deletes the run directory. The evidence stays. If a box won't go, it keeps the run directory and says so. Close any Rex panes you opened first. Run cleanup after a failed attempt too: a box left running holds up to 8 GB.

## Gotchas

- A box here reaches the user's real hallpass broker. A `bunk up` box, and a `bunk run claude` box while Claude runs, hold the user's real Claude token. Keep a proof to reads: no `git push`, no `gh` call that writes.
- Leave `codex-auth.enc` out of the run's home. Codex rotates its refresh token, so a scratch box that refreshed the login and was then deleted would sign the user's real boxes out. Codex here runs signed out. `bunk codex-login` needs a person with a browser and writes the real Keychain: don't drive it.
- `bunk run` copies Codex sessions back into `~/.codex/sessions`. Cleanup doesn't remove them, so name any a proof makes in the report.
- Keep `BUNKV` under `/tmp`. A box's sockets live under it, and a socket path is capped at 104 bytes on a Mac.
- On a Mac the box's home is the host's (`/Users/NAME`), and a scratch worktree sits at the same `/private/tmp/...` path in the box.
- Proven on a Mac with vfkit only. On Linux, `bunkv launch` copies Firecracker and `vmlinux` from the installed home and reads the Claude token from there, and none of that has run. CI boots a box in Firecracker with `OFFLINE=1`.
