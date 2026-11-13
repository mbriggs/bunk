---
name: verify-copy-back
description: TLA+ check that bunk never throws away a box's work or the host's commits. Use when a diff touches task.Run, copyIn, copyOut, task.Remove, lockRun, the unsynced file, box.GC, Box.Remove, Box.Up, or Box.Running.
---

# Verify the copy-back contract

**Property.** Work an agent did in a box is never copied over or deleted unless the user passes `--force` or names the box in `bunk rm NAME`, and the copy back moves the host's branch only if the branch is still on the commit the box started from.
**Harm if broken.** The user loses what the agent did (often hours of uncommitted work), or loses their own commits from the branch, with no error.

The README promises this under "Use it": a box whose work didn't come back is held until `bunk rm --dir DIR --force`, and "the branch moves only if nothing moved it on the host meanwhile".

The model is `verify/copy-back/CopyBack.tla`. It has one directory's box, two `bunk run`s, `bunk rm --dir`, `bunk gc`, and the user moving the branch on the host at any moment. Five rules, each named for who must not do the harm:

| Rule | In plain words |
| --- | --- |
| `RunNeverCopiesOverHeldWork` | `bunk run` never copies the host's files over work the box still holds |
| `RunNeverRemovesHeldWork` | `bunk run`'s cleanup after a failed start never deletes a box that holds work |
| `RmDirNeverRemovesHeldWork` | `bunk rm --dir` without `--force` never deletes a box that holds work |
| `GcNeverRemovesHeldWork` | `bunk gc` never deletes a box that holds work |
| `BranchMovesOnlyFromWhatTheBoxCopied` | the copy back moves the host's branch only from the commit the box was given |

A box "holds work" (`held` in the model) from when a command runs in it until a copy back finishes. `held` and `harm` aren't in the code: they record what the rules are about. The code's own record is the box's `unsynced` file, which `task.Run` writes before the command and removes after the copy back. The rules hold only while `unsynced` is there whenever `held` is.

## Does the change touch it

Last confirmed against the code at the commit that made `bunk gc` skip a box with an `unsynced` file (its parent is `94f5903`). `check.sh` hashes `task/task.go`, `box/box.go`, `box/run.go`, and `main.go`, and reports `STALE` when one changes. Check the model when a diff changes any of these:

| Code | Model action |
| --- | --- |
| `task.Run` in `task/task.go`: `b.Exists()`, `b.Create` | `RunLooks`, `RunCreates` |
| `lockRun`, and where `task.Run` and `task.Remove` call it | `RunLocks`, `RmLocks` |
| `b.Up()` in `task.Run`, and `if created && !b.Running() { b.Remove() }` | `RunUp`, `RunUpFails`, `RunUpFailsLate` |
| the `unsynced` file in `task.Run`: the read, the write before the command, the write after `copyOut` fails, the remove after it succeeds | `RunChecksMarker`, `RunMarksHeld`, `RunCopyOutFails` (`LeaveHolding`), `RunCopiesBack` |
| `copyIn`: the `refs` read at its start, and the `rsync --delete` loop | `RunReadsRefs`, `RunCopiesIn`, `RunCopyInFails` |
| `GiveClaudeToken` and `TakeClaudeToken` before `copyIn` in `task.Run` | `RunCopyInFails` (a failure there returns before anything is copied) |
| `s.interactive` and its signal handling | `RunCommand`, `RunKilled` |
| `copyOut`: the order of its steps, `git update-ref -- branch sha old` in `moveBranch`, the working tree rsync | `RunMovesBranch`, `RunCopiesBack`, `RunCopyOutFails` |
| `task.Remove` without force | `RmLooks`, `RmLocks`, `RmChecksMarker`, `RmRemoves` |
| `box.GC` in `box/box.go`: which boxes it skips (running ones, and ones with an `unsynced` file) | `GcLooks`, `GcRemoves` |
| `Box.Remove`, `Box.Stop`, `Box.Running`, `Box.Create` in `box/box.go` | `RemoveBox`, `running`, `RunCreates` |
| `Box.Up`, `Box.Start`, the supervisor's lock in `Box.Run`, in `box/run.go` | `RunUp*`, `running`, `VMStopsByItself` |
| the `run`, `rm`, and `gc` cases in `main.go` | which commands exist |

A change elsewhere in those files doesn't touch the model: what gets copied (Claude and Codex config, sessions), `mirrorSessions`, the ssh config, the vsock channels, the limit on running boxes, `bunk up`, `bunk ssh`. A new command that deletes a box, copies into one, or moves a host ref is a new action even if it lives in another file.

The model treats these as one step each, because each is one in the code:
- `Box.Create`: it builds the box aside and renames it into place.
- `lockRun`: one `flock`. The lock file is inside the box's directory, so removing the box frees it, and the kernel frees it when its holder dies.
- `git update-ref -- branch sha old`: git's own compare-and-swap.
- `Box.Remove`: the model does `Stop` and `RemoveAll` together. A process killed between them isn't modeled.
- `Box.Up` and the cleanup after it fails: the run holds the run lock throughout.
- The end of `copyOut` and `os.Remove(unsynced)`: two steps in the code. A run that dies between them leaves a box held with nothing in it to lose, which breaks no rule.

Every command finds the box by path. So in the model, a command that started on a box that was removed and made again carries on against the new one.

## Make the model describe the change first

`check.sh` checks the model, not the Go code. When a diff changes a step in the table, edit `CopyBack.tla` to describe the code after the diff, then run. Until you do, a green run says nothing about the change. Do this whether the change is intended or you suspect a bug. If you're only reviewing, edit a copy of `verify/copy-back/` outside the repo and run its `check.sh` there (the drift check will complain about paths; ignore it).

Each action's comment names its code. A removed check becomes a removed conjunct or branch. A reordered pair of steps becomes reordered `pc` values. A new call site is a new action with its own `pc` value.

## Run

```sh
verify/copy-back/check.sh     # about 10 seconds
```

It needs Java 11 or later. The first run downloads tla2tools 1.7.4 to `~/.cache/bunk/` and checks its hash. Set `TLA2TOOLS_JAR` to use a copy you have. Exit 0 means every scenario gave its expected result and the covered files are unchanged. Exit 1 means a result changed. Exit 3 means results as expected but covered code changed (`STALE`).

Each scenario is a `.cfg` with two runs and up to two branch moves on the host, at any moment. `Base` assumes nothing else goes wrong around bunk. Each other one adds a single thing that can:

| Scenario | Adds | Expected |
| --- | --- | --- |
| `Base` | nothing. gc runs only while no `bunk run` or `bunk rm --dir` is partway through | every rule holds |
| `KilledRun` | a `bunk run` can die at any step | every rule holds |
| `VMStops` | the box's VM can stop by itself, or by `bunk stop NAME` | breaks `RunNeverRemovesHeldWork` |
| `GcRaces` | gc runs at any moment, and other commands take steps between its look at a box and its removal | breaks `GcNeverRemovesHeldWork`, `RmDirNeverRemovesHeldWork`, `RunNeverCopiesOverHeldWork` |

The breaks in `VMStops` and `GcRaces` are findings about bunk as it is at the sync commit, listed below. `check.sh` fails if a scenario breaks a rule that isn't in its list, or stops breaking one that is.

## Read a result

For any `FAIL`, read the path before you decide anything. Read `~/.claude/skills/statechart/SKILL.md` (or `~/.agents/skills/statechart/SKILL.md` outside Claude Code) and follow it on `CopyBack.tla` with the scenario's `.cfg`, in a scratch folder outside the repo. It gives the shortest path to each rule that breaks, and a chart of the states when none does. If neither file exists, read TLC's own output in `verify/copy-back/out/SCENARIO-RULE.txt`: each `State N: <Action ...>` line is one step.

Tell the path to the user as steps in bunk's words, one per action: "the first `bunk run` starts its command", "`bunk run` is killed", "the second `bunk run` copies in". Leave out TLA+ names.

Then decide which it is:

- **A real bug.** Each step of the path is something the code at this diff does, in that order. Open the code each action names and confirm it. Report the path. Don't weaken the rule, add an assumption to `Base`, or add the rule to a scenario's expected list to get a pass. Don't change product code unless the user asked.
- **A modeling error.** A step can't happen in the code: the model lets two things interleave that one lock or one syscall makes indivisible, or it dropped a check the code has. Fix the model, say what you changed and why, and rerun.
- **A finding that went away.** A scenario stops breaking a rule in its list. If the diff fixed the finding, remove the rule from that scenario's line in `check.sh`'s `expect` and from this skill. If not, the model lost the path: find out why.

## Known findings

Still open at the sync commit. Don't "fix" the model to hide them.

1. **`bunk run`'s cleanup can delete a held box** (`VMStops`). It needs a run paused between `b.Create` and `lockRun` while another run finishes with work held, then a VM stop and a failed start. Not replayed; very unlikely.
2. **gc takes no lock** (`GcRaces`). `run.lock` is inside the directory gc removes, and gc looks and removes in two steps. Each path needs gc to hit a window of microseconds as a run starts, and then stall. Not replayed; very unlikely.

Three findings were fixed at or before the sync commit, and the model guards each. If one comes back, `check.sh` fails:

- `bunk gc` didn't read `unsynced`, so it deleted a box that held work once its VM had stopped (a host reboot, or `bunk stop`) and it was a week unused, or at once if its directory was gone. `box.GC` now skips a box with the file. `VMStops` checks it.
- A `bunk run` that died left no `unsynced` file, so the next run copied over the box's work and `bunk rm --dir` deleted it. `task.Run` now writes the file before the command starts. `KilledRun` checks it.
- `copyIn` read the host's branch after it copied the repository, so a commit made on the host in between was dropped from the branch at the copy back. It now reads the branch first. Every scenario checks it, since the host's branch moves at any moment in all of them.

## When a change is intended

1. Edit `CopyBack.tla` to describe the new code, with each action's comment naming it.
2. Run `check.sh`. Read every changed result as above.
3. If the change fixes a finding, update `expect` in `check.sh`, the scenario table, and the findings list here.
4. Update the coverage table here, and `covered` in `check.sh` if files were added.
5. Only after you've confirmed the model describes the code at the new commit: run `verify/copy-back/check.sh --resync`, and change the sync commit in this file to that commit.

To see that a rule can still fail, break its guard in a scratch copy of the model and run `Base`. Each of these broke a rule in `Base`: run skips the `unsynced` check, run removes `unsynced` when the copy back fails, run skips `lockRun`, run's cleanup drops `!b.Running()`, `rm --dir` skips `lockRun`, `rm --dir` skips the `unsynced` check, gc drops the `Running` check, gc drops the `unsynced` check (in `VMStops`), `update-ref` without the old value, and `copyIn` reads `refs` after the rsync. Dropping the `unsynced` write before the command broke a rule in `KilledRun`.

## What this doesn't cover

- The Go code itself. The model is a hand description of it, kept in step by the table and the hashes.
- More than one box, more than two runs, or more than two branch moves.
- Two faults at once. Each scenario adds one.
- `bunk rm NAME`, `--force`, and `bunk up NAME`.
- What the copies carry: whether rsync's flags, excludes, and paths bring back the right files, and whether `.git` hooks and config stay out.
- Work the box hasn't flushed to its disk when the VM stops.
- A branch the box switched to, a detached `HEAD`, and directories outside git. `moveBranch` refuses a move of a branch the worktree wasn't on, and anything from the box that isn't a branch and a commit; `task/task_test.go` checks both.
- Host files changed in the worktree while the box ran. The copy back overwrites them by design.
- Liveness: nothing here says a run finishes or a held box is ever released.
