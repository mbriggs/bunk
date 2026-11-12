# bunk

bunk runs throwaway sandbox VMs, called boxes, on a build machine: one box per task. A box has no network device. Everything it reaches goes over vsock to its supervisor on the host:

- sshd, which ferry and git use to get in.
- An HTTPS proxy that allows only the names in an allowlist.
- The Claude API, with the host's OAuth token swapped in for a sentinel, so no credential enters the box.
- The host's hallpass broker, for git and gh against GitHub.

Inside, Claude runs without permission prompts, because the VM is the boundary. You hand a task in and out with `ferry to HOST --sandbox` and `ferry from TASK.HOST.box`.

A Linux build machine runs boxes in Firecracker. A Mac runs them in vfkit (Virtualization.framework).

## Set up a Linux build machine

You need KVM, a home directory on btrfs or xfs (a box's disk is a reflink copy of the image), podman, and hallpass with your uid in its `uids`. Boxes share your hallpass.

1. From this checkout on the laptop, install bunk on the build machine. The image also takes the build machine's `~/.local/bin/ferry`, which `ferry setup` or `ferry push` installs:

   ```sh
   GOOS=linux GOARCH=amd64 go build -o bunk . && scp bunk HOST:.local/bin/
   ```

2. On the build machine, install the pinned Firecracker and guest kernel into bunk's home, `~/.local/share/bunk`. The script checks both against their sha256:

   ```sh
   ./image/fetch-firecracker.sh
   ```

3. Give boxes the allowlist, the mode, and the public keys that may log in to them. The mode is `allowlist`, or `open` to allow any name but GitHub's. Use the public half of the key the laptop's ssh config names below:

   ```sh
   cp allow.txt ~/.local/share/bunk/
   echo allowlist > ~/.local/share/bunk/mode
   cat laptop_key.pub > ~/.local/share/bunk/authorized_keys
   ```

4. Copy `image/` to the build machine, and build the image there. It takes about a minute:

   ```sh
   ./image/build.sh
   ```

## Set up a Mac build machine

You need vfkit (`brew install vfkit`), Docker (OrbStack works), Go, and checkouts of hallpass in `~/src/hallpass` and ferry in `~/src/ferry`, with `make dist` run in ferry. `build.sh` cross-builds bunk and hallpass for the box from those checkouts.

1. Build bunk in this checkout (`go build`), and install it in `~/.local/bin` or `~/.local/share/bunk/bin`. ferry looks in both.
2. Give boxes the allowlist, mode, and `authorized_keys`, as in step 3 for Linux.
3. Build the image from this checkout. On a Mac it also writes the kernel (`Image`) and the empty initrd vfkit requires:

   ```sh
   ./image/build.sh
   ```

## Set up the laptop

Add one block per build machine to `~/.ssh/config`. It routes every box on that machine through `bunk proxy`. Replace the user, the build machine, and the key with yours:

```
Host *.kant.box
  User mbriggs
  IdentityFile ~/.ssh/box_key
  IdentitiesOnly yes
  UserKnownHostsFile ~/.ssh/bunk/%h
  StrictHostKeyChecking yes
  ProxyCommand ssh mbriggs@kant .local/bin/bunk proxy %h
```

For a box on the laptop itself, run bunk directly: `ProxyCommand ~/.local/share/bunk/bin/bunk proxy %h`. ferry pins each box's host key in a file of its own, `~/.ssh/bunk/NAME.HOST.box`, when it makes the box, so `StrictHostKeyChecking yes` never asks and parallel handoffs never share a file. ferry refuses a block whose `UserKnownHostsFile` doesn't end in `%h`.

## Use it

To hand the current task to a fresh box on kant, run this in its worktree:

```sh
ferry to kant --sandbox
```

ferry runs `bunk up` on kant to make a box named after the task: the worktree's name plus a short hash of its path, like `fix-auth-3f9c2a`. It moves the worktree and the Claude session in, and resumes Claude there in Rex. ferry prints the box's ssh name. To bring the task back and delete its box:

```sh
ferry from fix-auth-3f9c2a.kant.box
```

To start new tasks in parallel, make a worktree for each and start Claude in a box with a prompt:

```sh
git worktree add .claude/worktrees/fix-login -b fix-login
cd .claude/worktrees/fix-login
ferry to kant --sandbox --prompt "Fix the login redirect and commit it."
```

`ferry from fix-login-HASH.kant.box`, with the name ferry printed, brings back the branch and the session Claude made.

On the build machine, `bunk ls` lists boxes and `bunk rm NAME` deletes one. `bunk up NAME` makes a box, or starts it again if it exists.

## Check a box

After changing the image, the kernel, or bunk, check a running box from the laptop:

```sh
./check.sh NAME.kant.box
```

Each line is one channel: egress, apt, the model endpoint, hallpass, the clock, and Claude editing and committing with no prompt. A line starts with `ok`, `FAIL`, or `skip`, and the exit status is the number of failures. `OFFLINE=1` skips the checks that need the build machine's Claude credentials or hallpass broker. CI (`.github/workflows/ci.yml`) boots a box in Firecracker on every push and runs the checks that way.

## Change the allowlist

Edit `~/.local/share/bunk/allow.txt` on the build machine. The next request reads it. A line is a name, `.domain` for a domain and its subdomains, or `~regexp`, which must match the whole name. Each box's `supervisor.log` lists refused names. GitHub's own names are always refused, because git and gh reach GitHub only through hallpass.

## What's in a box's directory

Each box lives in `~/.local/share/bunk/boxes/NAME`, which `bunk rm` deletes:

- `disk.ext4`: the box's copy of the image.
- `identity.tar`: a second disk with the box's hostname, its SSH host key, and `authorized_keys`, unpacked at boot.
- `host_key.pub`: the host key, for known_hosts.
- `supervisor.log`: the supervisor's log, including egress decisions.
- `vm.log`: the VM's console on Linux, and vfkit's own log on a Mac.
- `console.log`: the VM's console on a Mac.
- `v.sock*`: the vsock sockets, and `lock`, which the supervisor holds while it runs.

## Limits

- Docker in a Firecracker box needs `--network host`. The CI kernel lacks the netfilter options Docker's bridge networking uses.
- The image pins Claude, Codex, and Rex in `image/versions`. ferry won't move a session between different Claude versions in either direction, so the pins follow the laptop: when its Claude updates, run `./image/bump.sh` on the laptop, commit `image/versions`, and rebuild the image on each build machine.
- If a Mac box's supervisor is killed, vfkit keeps running. On Linux the VM dies with its supervisor.
