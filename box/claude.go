package box

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// A box's Claude signs in with the user's own token from `claude setup-token`,
// the secret "claude-token" (see LoadSecret), which bunk puts in the box's
// managed Claude settings while Claude runs there, and takes out after.
// Claude Code itself then authenticates, as on any machine; nothing between
// it and Anthropic holds or swaps the credential. While the token is in the
// box, a box gone wrong could use it: revoke it at claude.ai if that happens.

const managedSettings = "/etc/claude-code/managed-settings.json"

// GiveClaudeToken writes the token into the box's managed settings. It's
// sent on ssh's stdin, never in a command line. Without a token it does
// nothing, and Claude in the box isn't signed in.
func (b Box) GiveClaudeToken() error {
	raw, err := LoadSecret("claude-token")
	tok := strings.TrimSpace(string(raw))
	if err != nil || tok == "" {
		return err
	}
	// jq reads the token from stdin, so it's in no process's arguments here
	// or in the box.
	return b.editSettings(`--rawfile t /dev/stdin ".env.CLAUDE_CODE_OAUTH_TOKEN = \$t"`, tok)
}

// TakeClaudeToken takes the token out of the box's managed settings.
func (b Box) TakeClaudeToken() error {
	return b.editSettings(`"del(.env.CLAUDE_CODE_OAUTH_TOKEN)"`, "")
}

// editSettings rewrites the box's managed settings with jq's args, as root.
func (b Box) editSettings(args, stdin string) error {
	cfg, err := b.SSHConfig()
	if err != nil {
		return err
	}
	script := `sudo sh -c 'umask 022 && jq ` + args + ` ` + managedSettings + ` > ` + managedSettings + `.new && mv ` + managedSettings + `.new ` + managedSettings + `'`
	cmd := exec.Command("ssh", "-F", cfg, b.Name, script)
	cmd.Stdin = strings.NewReader(stdin)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("box %s's Claude settings: %v %s", b.Name, err, strings.TrimSpace(errb.String()))
	}
	return nil
}
