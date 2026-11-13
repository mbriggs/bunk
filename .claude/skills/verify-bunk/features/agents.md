# Agents in a box

`bunk run claude` runs Claude Code in the directory's box, on the user's terminal, with no permission prompts. The box's Claude has the host's instructions, settings, skills, and plugins, and signs in with the user's token. Its sessions come back to the host, and while it runs the host sees the session as bunk's own. `bunk run codex` does the same for Codex.

## Sub-features

- `agent-start` Claude starts in the box signed in, at the worktree's path, with no trust or permission prompt.
- `agent-work` Claude edits and commits without asking, and the work is on the host after it exits.
- `agent-config` the host's `~/.claude` config is in the box: a host skill is usable there.
- `agent-mirror` while Claude runs, `~/.claude/sessions/bunk-PID-SESSION.json` exists on the host with bunk's pid and Claude's status; it's gone after the copy back.
- `agent-term` SIGTERM to `bunk run` ends Claude in the box and copies the work back before bunk exits.
- `agent-session` Claude's session file for the directory arrives in the host's `~/.claude/projects/SLUG/`.
- `agent-creds` the Claude token is in the box only while Claude runs there: a run of any other command sees none, and none is left after a Claude run.
- `agent-print` `bunk run claude -p PROMPT` works with no terminal.
- `agent-codex` `bunk run codex` starts Codex with approvals and its sandbox off.

## How to get to it (user POV)

- `bunk run claude` in a worktree, in a Rex pane.
- `bunk run claude -p PROMPT` from any shell.
- `bunk run claude --resume ID`, which is what ferry starts.
- `bunk run codex` in a worktree, in a Rex pane.
- SIGTERM to the `bunk run` process, which is how ferry stops an agent.

## Driving it with bunkv

Preconditions:

- The baseline. `$v doctor` notes a Claude token. Without one, Claude in the box isn't signed in: report every sub-feature here unverified.
- A Rex session (`$REX_SESSION` is set) for the pane entry points. `r=$($v repo agent)`, `E=$($v evidence)`.

- **Start in a pane.** Run `rex run -s "$REX_SESSION" --focus=false --label bunkv-agent -c "$r" --keep-open --json -- env BUNK_HOME="$BUNKV/home" "$BUNKV/bin/bunk" run claude`, and keep `.block_ids[0]` as `BLOCK`. Poll `rex capture -b BLOCK --trim` until it shows `bypass permissions on`. The header shows `Claude Code`, a model name, and `$r`. Save the capture to `$E/agent-start.txt`.
- **Ask for work.** Run `rex send -b BLOCK 'Create a file named hello.txt containing exactly the word hi, then commit it with the message hello.' && rex send-key -b BLOCK enter`.
- **The mirrored session.** Poll `cat ~/.claude/sessions/bunk-*.json` until one has `"status":"idle"` after having been busy. Each is `{"pid":P,"sessionId":"...","status":"...","via":"bunk"}`, and `ps -o command= -p P` prints `$BUNKV/bin/bunk run claude`. Save both outputs to `$E/agent-mirror.txt`. Use only the entry whose pid passes that `ps` check: the user's own boxes write entries with the same prefix.
- **Claude's side.** Run `rex capture -b BLOCK --trim | grep -v '^\s*$' > "$E/agent-work.txt"`. It shows a `Write(hello.txt)` and a commit, with no permission question between them. If the host has a `commit` skill, `Skill(commit)` shows there too, which proves `agent-config`; otherwise send `/skills` or ask Claude to list its skills and compare with the host's `~/.claude/skills`.
- **Not home yet.** Run `$v do agent-before sh -c 'ls; git log --oneline'` in `$r`. Only `README` and `seed`: the work comes back when the command ends.
- **Stop it with SIGTERM.** Run `kill -TERM P`, and wait for `P` to exit (about 2 seconds). Then `$v do agent-after sh -c 'cat hello.txt; echo; git log --oneline; git status --short; ls ~/.claude/sessions | grep -c "^bunk-P-"; ls ~/.claude/projects/$(printf %s "$PWD" | sed "s/[^a-zA-Z0-9]/-/g")'` in `$r`. It prints `hi`, a log with `hello` above `seed`, an empty status, `0`, and a `SESSION.jsonl` named for the mirrored session ID.
- **Close the pane.** Run `rex block close BLOCK`.
- **Print mode.** Run `$v do agent-print bunk run claude -p 'Append the line b to the file README and commit it with the message b.'` in `$r`, then `$v do agent-print-host sh -c 'tail -1 README; git log --oneline -1'`. `b`, and a commit `b`.
- **Credentials.** Run `$v do agent-creds bunk run sh -c 'sudo jq -r ".env | has(\"CLAUDE_CODE_OAUTH_TOKEN\")" /etc/claude-code/managed-settings.json; ls ~/.codex/auth.json'` in `$r`, after the print-mode run. It prints `false`, and `ls` reports no such file.
- **Resume.** Start a pane as above with `run claude --resume SESSION`, using the mirrored session ID. The capture shows the earlier prompt about `hello.txt`. Exit with `rex send -b BLOCK /exit && rex send-key -b BLOCK enter`, and check that `bunk run` exited 0 in the pane.
- **Codex.** Start a pane with `run codex`. Codex runs signed out here, so the proof stops at its sign-in screen: report `agent-codex` as unverified past start, with the capture. Testing Codex signed in means the installed bunk and the user's say-so.
- **Leave it clean.** Run `$v do agent-rm bunk rm --dir "$r"`.

## Gotchas

- Each prompt spends the user's Claude usage. Keep prompts as small as these.
- The box's Claude loads the host's skills and `CLAUDE.md`, so its wording and tool calls vary with the host's config. Assert on the file, the commit, and the session file, not on Claude's prose.
- The capture of a full-screen program is mostly blank lines. Filter them before reading or saving.
- `rex run --keep-open` leaves the pane after bunk exits, so the exit can be read. Close it yourself.
- The session folder in `~/.claude/projects` is evidence that `bunkv cleanup` moves out of the host's folder. Check it before cleanup.
- `bunk run codex` copies Codex sessions back into `~/.codex/sessions`, which cleanup leaves.
