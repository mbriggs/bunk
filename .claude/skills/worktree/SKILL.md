---
name: worktree
description: How to work on bunk in a lane, a linked git worktree with its own bunk home, boxes, and bunk binary. Use before the first write in the primary checkout, when the user asks for a parallel session, and whenever the working directory is under .claude/worktrees/. Covers creating a lane, running bunk and the landing check there, landing through a PR, merging, and cleanup.
---

# Working in a lane

Make changes in a linked worktree, not in the primary checkout. Each worktree is a **lane**: `bin/worktree` gives it an index `n` (0 to 9) and its own bunk home, `~/.local/share/bunk-lane-<n>`, beside the primary's `~/.local/share/bunk`. `bin/worktree.lane` holds what a lane isolates. `bin/worktree info` prints the current checkout's values.

## Create a lane

- In this session: call `EnterWorktree`.
- For a subagent: pass `isolation: "worktree"`.
- From a terminal: `claude --worktree NAME`.
- Anything else: `git worktree add .claude/worktrees/NAME -b NAME`, then `bin/worktree setup` in it.

The first Bash command in a new worktree provisions the lane and reports its values. Provisioning clones the primary's box image (instant on APFS), copies its `allow.txt` and `mode`, and builds bunk from the lane's source. If it fails, read `tmp/worktree-setup.failed`, fix the cause, and run `bin/worktree setup`.

## What a lane has

| | In a lane |
| --- | --- |
| `BUNK_HOME` | `~/.local/share/bunk-lane-<n>`: the lane's boxes, SSH key, image, and limit of 2 running boxes |
| `LANE_BUNK` | `$BUNK_HOME/bin/bunk`, built from the lane's source |
| Box image | A clone of the primary's. `./image/build.sh` in the lane rebuilds it into the lane's home, under the Docker tag `localhost/bunk-box-lane-<n>` |
| Claude token, hallpass broker | Shared with the primary. A lane's box pushes to the real GitHub |
| Codex | No login. Don't run `bunk codex-login` in a lane, and don't copy the primary's `codex-auth.enc`: a lane box refreshing it would sign the primary's boxes out. Test `bunk run codex` from the primary |

## Commands

- Run the lane's bunk as `"$LANE_BUNK"`, never a bare `bunk`, which is the primary's installed build or nothing. After you change Go code, rebuild it: `go build -o "$LANE_BUNK" .`
- `"$LANE_BUNK" up NAME`, `ls`, `rm NAME`, and `run` all act on the lane's home. Remove the boxes you make. Teardown stops and deletes any that are left.
- `bin/install` in a lane rebuilds the lane's bunk and, if it's stale, the lane's image. With a host or `--all` it refuses in a lane.
- `./check.sh BOX` and `./image/build.sh` load the lane themselves and refuse to start in a worktree with no lane.
- `go test ./...` touches nothing outside the checkout.
- After you change `image/`, `relay/`, `clock/`, `main.go`, or `go.mod`, the lane's image is stale. `bin/verify` rebuilds it, or run `./image/build.sh` (about a minute, needs Docker running).

## The working directory

The env hook picks the checkout from the directory a command starts in. `cd <lane> && cmd`, run from the primary, gets the primary's env, so `BUNK_HOME` is unset and bunk uses the primary's home. Run lane commands with the lane as the working directory.

## Commands that run in the primary

`bin/worktree merge`, `sync`, and `teardown NAME` run in the primary. A session isolated to a worktree can't run git against the primary:

- A session that entered the lane with `EnterWorktree`: call `ExitWorktree` with `keep`, run the command from the primary, and re-enter with `EnterWorktree` and the lane's path.
- A subagent: return the command to its parent to run.
- A session started with `claude --worktree`: ask the user to run it from the primary.

## Land

A lane lands as one commit in a squash-merged PR. Run gh as `/opt/hallpass/bin/hallpass gh`.

1. Commit everything. Run `bin/worktree land` early if you like: it refuses and says what's missing.
2. If `land` says the lane lacks commits from `origin/main`, run `git rebase origin/main`. If it also names lane tooling that changed, reread this skill.
3. Run `bin/verify` in the lane. It runs gofmt, vet, and the tests, builds the lane's bunk, rebuilds the lane's image if the change touched what it is built from, boots a box, and runs `check.sh` on it. It takes a minute or two, longer with an image build, and stamps the committed tree when everything passes. A Markdown-only change needs no run. `OFFLINE=1` writes no stamp. If the change can't affect what the check covers, `land --no-verify REASON` puts the reason in the PR.
4. Write the commit message to a file outside the checkout, and the PR description to another. Write the description with the prose skill, for the reviewer as the reader, and use the show-me skill for views that make the change clear.
5. `bin/worktree land --message MSGFILE --body BODYFILE`. It pushes the branch and opens or updates the PR.
6. Own the PR until it merges. Watch CI in the background: `/opt/hallpass/bin/hallpass gh pr checks BRANCH --watch`. Fix failures and review comments in the lane, then land again from step 2.
7. When the checks are green, merge it: from the primary, `bin/worktree merge NAME`. The user has decided that agents merge every green PR, so "land it" includes the merge here. Always merge through `bin/worktree merge`, never `gh pr merge`.
8. `merge` then syncs: it fast-forwards the primary's `main` and runs `bin/install` there, which installs bunk into `~/.local/share/bunk/bin` and rebuilds this machine's image if the merge changed what it is built from. If the user merged the PR themselves, run `bin/worktree sync` from the primary. Relay what it prints:
   - `NOT INSTALLED`: give the user the command it names to run in the primary. An image build needs Docker running.
   - `THE BUILD MACHINES ARE STALE`: the user runs `bin/install --all` in the primary, or `bin/install HOST`. Don't run it yourself unless asked: it changes other machines.
   - Boxes made from an older image: tell the user which, since removing a box is theirs to decide.

To keep working in the lane after a squash merge, run `git fetch origin main && git rebase --onto origin/main <merged PR head>`. A plain rebase replays the squashed commits.

## Clean up

Ask the user before you remove a lane.

1. In a session that entered with `EnterWorktree`, call `ExitWorktree` with `keep`, never `remove`: `remove` refuses a worktree with commits made since it was created, even landed ones.
2. From the primary: `bin/worktree teardown NAME`. It removes the worktree and its branch, stops and deletes the lane's boxes, removes the Docker image the lane built, and deletes its bunk home. It refuses uncommitted or unlanded work unless you pass `--discard`, which needs the user's agreement.
3. Teardown never syncs. If it says `main` is behind `origin/main`, run `bin/worktree sync`.

Teardown run from inside the lane leaves the directory behind, and the next command provisions the lane again.
