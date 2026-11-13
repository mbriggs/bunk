package box

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// A box's Claude signs in with the user's own token from `claude setup-token`,
// the secret "claude-token" (see LoadSecret), which bunk puts in the box's
// managed Claude settings each time the box starts. Claude Code itself then authenticates, as on any
// machine; nothing between it and Anthropic holds or swaps the credential.
// The token is in the box, so a box that went wrong could use it: revoke it
// at claude.ai if that happens.

// installClaudeToken writes the token into the box's managed settings. It's
// sent on ssh's stdin, never in a command line. Without a token it does
// nothing, and Claude in the box isn't signed in.
func (b Box) installClaudeToken() error {
	raw, err := LoadSecret("claude-token")
	tok := strings.TrimSpace(string(raw))
	if err != nil || tok == "" {
		return err
	}
	cfg, err := b.SSHConfig()
	if err != nil {
		return err
	}
	const settings = "/etc/claude-code/managed-settings.json"
	// jq reads the token from stdin, so it's in no process's arguments here
	// or in the box.
	script := `sudo sh -c 'umask 022 && jq --rawfile t /dev/stdin ".env.CLAUDE_CODE_OAUTH_TOKEN = \$t" ` + settings + ` > ` + settings + `.new && mv ` + settings + `.new ` + settings + `'`
	cmd := exec.Command("ssh", "-F", cfg, b.Name, script)
	cmd.Stdin = strings.NewReader(tok)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("giving box %s the Claude token: %v %s", b.Name, err, strings.TrimSpace(errb.String()))
	}
	return nil
}
