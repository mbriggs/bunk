# bunk

bunk runs an agent in a sandbox VM, called a box, from a terminal on the build machine. Run `bunk run claude` in a worktree, in a Rex pane: bunk copies the worktree into the worktree's own box, runs Claude there on that terminal, and copies the results back when Claude exits. The pane stays in the machine's own Rex, so a box's agent shows up beside every other session.

A box has no network device. Everything it reaches goes over vsock to its supervisor on the host:

- An HTTPS proxy that allows only the names in an allowlist.
- The Claude API, with the host's token swapped in for a sentinel, so no credential enters the box.
- The host's hallpass broker, for git and gh against GitHub.
- The host's clock, and sshd, which bunk uses to run commands and copy files.

Inside, Claude runs without permission prompts, because the VM is the boundary. It has the host's instructions, settings, skills, and plugins, copied in each run.

A Linux build machine runs boxes in Firecracker. A Mac runs them in vfkit (Virtualization.framework).

## Set up a Linux build machine

You need KVM, a home directory on btrfs or xfs (a box's disk is a reflink copy of the image), podman, and hallpass with your uid in its `uids`. Boxes share your hallpass.

1. From this checkout on the laptop, install bunk on the build machine. Install it beside the old binary and rename it into place, since a running box's supervisor keeps the old one open:

   ```sh
   GOOS=linux GOARCH=amd64 go build -o bunk . && scp bunk HOST:.local/bin/bunk.new && ssh HOST mv .local/bin/bunk.new .local/bin/bunk
   ```

2. On the build machine, install the pinned Firecracker and guest kernel into bunk's home, `~/.local/share/bunk`. The script checks both against their sha256:

   ```sh
   ./image/fetch-firecracker.sh
   ```

3. Give boxes the allowlist and the mode. The mode is `allowlist`, or `open` to allow any name but GitHub's:

   ```sh
   cp allow.txt ~/.local/share/bunk/
   echo allowlist > ~/.local/share/bunk/mode
   ```

4. Give the model proxy a token. A machine where Claude doesn't run every day can't rely on Claude's own credentials, which expire within hours unless that Claude refreshes them. Make a token that lasts about a year with `claude setup-token`, on any machine, and save it on the build machine:

   ```sh
   umask 077 && cat > ~/.local/share/bunk/claude-token    # paste the token, then ^D
   ```

5. Copy `image/` to the build machine, and build the image there. It takes about a minute:

   ```sh
   ./image/build.sh
   ```

## Set up a Mac build machine

You need vfkit (`brew install vfkit`), Docker (OrbStack works), Go, and a checkout of hallpass in `~/src/hallpass`. `build.sh` cross-builds bunk and hallpass for the box from the checkouts. The model proxy uses the Mac's own Claude credentials, from the Keychain.

1. Build bunk in this checkout (`go build`), and install it in `~/.local/bin` or `~/.local/share/bunk/bin`.
2. Give boxes the allowlist and mode, as in step 3 for Linux.
3. Build the image from this checkout. On a Mac it also writes the kernel (`Image`) and the empty initrd vfkit requires:

   ```sh
   ./image/build.sh
   ```

## Use it

In a worktree on the build machine, in a Rex pane:

```sh
bunk run claude
```

bunk finds the worktree's box, named for the worktree and a hash of its path (like `fix-auth-3f9c2a`), and makes and boots it the first time. Each run copies in the worktree, its git repository, Claude's sessions for the directory, and the host's Claude config, all to the same paths: the box's home is the host's, even on a Mac. When the command exits, bunk copies back the working tree, the branch, and Claude's sessions. `bunk run` takes any command: `bunk run npm test` works too.

The copy back trusts the box with this worktree and nothing else. git objects only add. The branch moves only if nothing moved it on the host meanwhile. The rest of `.git`, such as hooks, config, and other branches, never comes back. If the branch moved, nothing but Claude's sessions comes back. bunk names the box's commit, which is already in the repository for you to merge or cherry-pick, and holds the box: the next `bunk run` there refuses instead of copying over it, until you `bunk rm --dir DIR`.

The box stays between runs, so installed dependencies stay too. `bunk rm --dir DIR` deletes a directory's box. It refuses while a `bunk run` is using the box, or while the box holds work that didn't come back, unless you add `--force`. `bunk gc` deletes stopped boxes unused for a week, and stopped boxes whose directory is gone.

### With ferry

To have ferry start every agent on a build machine in its box, put the launch prefix in that machine's `~/.config/ferry/launch`:

```
bunk run
```

And put the teardown command in `~/.config/ferry/teardown`, so `ferry teardown` deletes a task's box along with its worktree:

```
bunk rm --dir
```

ferry then starts `bunk run claude --resume ...` in a pane of the machine's Rex. While a box's Claude runs, bunk shows its session to the host as its own, with bunk's pid. So ferry sees it as live and busy as usual. Stopping it with SIGTERM ends Claude in the box and copies the work back before bunk exits, and only then does `ferry from` move the task.

### Other commands

- `bunk ls` lists boxes, when each was last used, and how many run out of the limit.
- `bunk ssh NAME [CMD...]` logs in to a box, or runs a command there.
- `bunk up NAME` and `bunk rm NAME` make and delete a box by name, outside any directory.
- At most 8 boxes run at once. Put another number in `~/.local/share/bunk/max-running` to change that.

## Check a box

After changing the image, the kernel, or bunk, check a running box on its build machine:

```sh
bunk up c1 && ./check.sh c1; bunk rm c1
```

Each line is one channel: egress, apt, the model endpoint, hallpass, the clock, and Claude editing and committing with no prompt. A line starts with `ok`, `FAIL`, or `skip`, and the exit status is the number of failures. `OFFLINE=1` skips the checks that need the build machine's Claude token or hallpass broker. CI (`.github/workflows/ci.yml`) boots a box in Firecracker on every push and runs the checks that way.

## Change the allowlist

Edit `~/.local/share/bunk/allow.txt` on the build machine. The next request reads it. A line is a name, `.domain` for a domain and its subdomains, or `~regexp`, which must match the whole name. Each box's `supervisor.log` lists refused names. GitHub's own names are always refused, because git and gh reach GitHub only through hallpass.

## What's in a box's directory

Each box lives in `~/.local/share/bunk/boxes/NAME`, which `bunk rm` deletes:

- `disk.ext4`: the box's copy of the image.
- `identity.tar`: a second disk with the box's hostname, its SSH host key, and the keys that may log in, unpacked at boot.
- `workdir`: the directory the box is for, if `bunk run` made it.
- `ssh_config` and `known_hosts`: how bunk reaches the box, with its host key pinned.
- `supervisor.log`: the supervisor's log, including egress decisions.
- `vm.log`: the VM's console on Linux, and vfkit's own log on a Mac.
- `console.log`: the VM's console on a Mac.
- `v.sock*`: the vsock sockets, and `lock`, which the supervisor holds while it runs.
- `unsynced`: present when a run's work didn't come back; it says why.

## Limits

- Docker in a Firecracker box needs `--network host`. The CI kernel lacks the netfilter options Docker's bridge networking uses.
- The image pins Claude, Codex, and Rex in `image/versions`. ferry won't move a session between different Claude versions in either direction, so the pins follow the laptop: when its Claude updates, run `./image/bump.sh` on the laptop, commit `image/versions`, and rebuild the image on each build machine.
- While a command runs, the box's copy of the worktree and the host's differ. Changes made on the host meanwhile are overwritten by the copy back, except to the branch, which bunk refuses to move.
- Language-server plugins need their servers in the box, which the image doesn't have.
