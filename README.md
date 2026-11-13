# bunk

bunk runs an agent in a sandbox VM, called a box, from a terminal on the build machine. Run `bunk run claude` in a worktree, in a Rex pane: bunk copies the worktree into the worktree's own box, runs Claude there on that terminal, and copies the results back when Claude exits. The pane stays in the machine's own Rex, so a box's agent shows up beside every other session.

A box has no network device. Everything it reaches goes over vsock to its supervisor on the host:

- An HTTPS proxy that allows only the names in an allowlist.
- The host's hallpass broker, for git and gh against GitHub.
- The host's clock, and sshd, which bunk uses to run commands and copy files.

Inside, Claude runs without permission prompts, because the VM is the boundary. It has the host's instructions, settings, skills, and plugins, copied in each run, and signs in with your own token from `claude setup-token`. bunk puts the token in a box only while `bunk run claude` runs there, and takes it out after, so a box running anything else, or sitting idle, has no credential. The allowlist lets it reach `api.anthropic.com`. While Claude runs, a box gone wrong could use the token: revoke it at claude.ai if that happens.

A Linux build machine runs boxes in Firecracker. A Mac runs them in vfkit (Virtualization.framework).

## Set up a build machine

`bin/install` puts this checkout's bunk and box image where they run. Each step is skipped when it's already done, so run it again after any change to bunk or `image/`:

```sh
bin/install            # this machine
bin/install HOST...    # Linux build machines, over ssh
bin/install --all      # this machine, then every host in ~/.local/share/bunk/machines
```

It builds bunk and renames it into place, since a running box's supervisor keeps the old binary open. It gives a machine with no allowlist this checkout's `allow.txt` in `allowlist` mode, and leaves an existing one alone. It rebuilds the image, which takes about a minute, only when the image wasn't built from this tree's `image/` and guest code. Boxes made from an older image keep it until they're removed, and `bin/install` names them.

List your build machines, one ssh host a line, in `~/.local/share/bunk/machines`, and `bin/install --all` after a merge brings everything up to date.

The mode in `~/.local/share/bunk/mode` is `allowlist`, or `open` to allow any name but GitHub's.

### A Linux build machine

You need KVM, podman, and hallpass with your uid in its `uids`. On btrfs or xfs a box's disk is an instant reflink copy of the image; elsewhere it's a sparse copy, which takes a few seconds. Boxes share your hallpass.

1. Give boxes a Claude token, from `claude setup-token`, encrypted with systemd-creds under the machine's key and TPM, so it isn't on disk in the clear. This reads the token from stdin, here from 1Password:

   ```sh
   op read "op://Private/Claude Long OAuth/credential" | ssh HOST 'umask 077 && mkdir -p ~/.local/share/bunk && systemd-creds encrypt --user --with-key=host+tpm2 --name=bunk-claude-token - ~/.local/share/bunk/claude-token.cred'
   ```

   Without systemd-creds, a plain `~/.local/share/bunk/claude-token`, mode 0600, works too.

2. From this checkout on the laptop, run `bin/install HOST`. It installs bunk in `~/.local/bin`, the pinned Firecracker and guest kernel in bunk's home, `~/.local/share/bunk`, checked against their sha256, and the image, built on the machine from a copy of `image/`.

### A Mac build machine

You need vfkit (`brew install vfkit`), Docker (OrbStack works), Go, and a checkout of hallpass in `~/src/hallpass`. `build.sh` cross-builds bunk and hallpass for the box from the checkouts.

1. Give boxes a Claude token, from `claude setup-token`, in the login Keychain. This asks for the token instead of taking it on the command line:

   ```sh
   security add-generic-password -U -s bunk-claude-token -a "$USER" -w
   ```

2. Run `bin/install` in this checkout. It installs bunk in `~/.local/share/bunk/bin`, which goes on your `PATH`, and builds the image, with the kernel (`Image`) and the empty initrd vfkit requires.

## Use it

In a worktree on the build machine, in a Rex pane:

```sh
bunk run claude
```

bunk finds the worktree's box, named for the worktree and a hash of its path (like `fix-auth-3f9c2a`), and makes and boots it the first time. Each run copies in the worktree, its git repository, Claude's sessions for the directory, and the host's Claude config, all to the same paths: the box's home is the host's, even on a Mac. When the command exits, bunk copies back the working tree, the branch, and Claude's sessions. `bunk run` takes any command: `bunk run npm test` works too.

The copy back trusts the box with this worktree and nothing else. git objects only add. The branch moves only if nothing moved it on the host meanwhile. The rest of `.git`, such as hooks, config, and other branches, never comes back. If the branch moved, nothing but Claude's sessions comes back. bunk names the box's commit, which is already in the repository for you to merge or cherry-pick, and holds the box: the next `bunk run` there refuses instead of copying over it, until you `bunk rm --dir DIR`. A box is held the same way when its `bunk run` dies before the copy back is done.

The box stays between runs, so installed dependencies stay too. `bunk rm --dir DIR` deletes a directory's box. It refuses while a `bunk run` is using the box, or while the box holds work that didn't come back, unless you add `--force`. `bunk gc` deletes stopped boxes unused for a week, and stopped boxes whose directory is gone.

### Codex

`bunk run codex` works like `bunk run claude`. bunk copies in the host's Codex config (`AGENTS.md`, `config.toml`, skills, rules, and the rest) and Codex's sessions, and copies the sessions back, so `codex resume` finds them on either side. The box's managed config turns off Codex's approvals and its own sandbox, since the VM is the boundary.

A box's Codex signs in with a ChatGPT login of bunk's own, apart from the host's: Codex rotates its refresh token, so a box refreshing the host's login would sign the host out. Sign bunk in once on each build machine, with a device code you enter in any browser:

```sh
bunk codex-login
```

It runs `codex login` in a scratch box, so the host needs no Codex of its own, and keeps the login where it keeps secrets: the Keychain on a Mac, systemd-creds on Linux. Like Claude's token, the login is in a box only while `bunk run codex` runs there. When a box's Codex refreshes the login, bunk keeps the refreshed copy. Several boxes refreshing at the same moment can still sign it out; `bunk codex-login` again fixes that.

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
- `bunk up NAME` and `bunk rm NAME` make and delete a box by name, outside any directory. A box made this way is for trying things by hand, such as `check.sh`, so it has the Claude token for as long as it runs.
- At most 8 boxes run at once. Put another number in `~/.local/share/bunk/max-running` to change that.

## Check a box

After changing the image, the kernel, or bunk, check a running box on its build machine:

```sh
bunk up c1 && ./check.sh c1; bunk rm c1
```

Each line is one channel: egress, apt, hallpass, the clock, and Claude signing in, editing, and committing with no prompt. A line starts with `ok`, `FAIL`, or `skip`, and the exit status is the number of failures. `OFFLINE=1` skips the checks that need a Claude token or the build machine's hallpass broker. CI (`.github/workflows/ci.yml`) boots a box in Firecracker on every push and runs the checks that way.

## Change the allowlist

Edit `~/.local/share/bunk/allow.txt` on the build machine. The next request reads it. A line is a name, `.domain` for a domain and its subdomains, or `~regexp`, which must match the whole name. Each box's `supervisor.log` lists refused names. GitHub's own names are always refused, because git and gh reach GitHub only through hallpass.

The proxy sees only names, since a box's HTTPS is a tunnel it can't read. That's not enough for a shared host anyone can write to, like `storage.googleapis.com`, where allowing the name would let a box send data to a bucket of its own. So a line can be a name and a path instead, like `storage.googleapis.com/chrome-for-testing-public/`. For that host alone, the proxy ends the box's TLS itself, with a certificate from bunk's CA, and sends on only GETs and HEADs under the paths listed. bunk makes the CA the first time it needs it, in `~/.local/share/bunk/egress-ca.pem`, and each box trusts it from its identity drive, so a box made before the CA existed can't use path lines until it's remade. The CA's key stays on the host. Node gets the system's CAs through `NODE_EXTRA_CA_CERTS`; a tool with a CA list of its own, like Python's `certifi`, would need pointing at `/etc/ssl/certs/ca-certificates.crt` too.

## What's in a box's directory

Each box lives in `~/.local/share/bunk/boxes/NAME`, which `bunk rm` deletes:

- `disk.ext4`: the box's copy of the image.
- `identity.tar`: a second disk with the box's hostname, its SSH host key, the keys that may log in, and the egress CA's certificate, unpacked at boot.
- `workdir`: the directory the box is for, if `bunk run` made it.
- `ssh_config` and `known_hosts`: how bunk reaches the box, with its host key pinned.
- `supervisor.log`: the supervisor's log, including egress decisions.
- `vm.log`: the VM's console on Linux, and vfkit's own log on a Mac.
- `console.log`: the VM's console on a Mac.
- `v.sock*`: the vsock sockets, and `lock`, which the supervisor holds while it runs.
- `unsynced`: present from when a run's command starts until its work is back, so it stays when the work didn't come back; it says why.

## Limits

- Docker in a Firecracker box needs `--network host`. The CI kernel lacks the netfilter options Docker's bridge networking uses.
- The image pins Claude, Codex, and Rex in `image/versions`. ferry won't move a session between different Claude versions in either direction, so the pins follow the laptop: when its Claude updates, run `./image/bump.sh` on the laptop, commit `image/versions`, and rebuild the image on each build machine.
- While a command runs, the box's copy of the worktree and the host's differ. Changes made on the host meanwhile are overwritten by the copy back, except to the branch, which bunk refuses to move.
- Language-server plugins need their servers in the box, which the image doesn't have.
