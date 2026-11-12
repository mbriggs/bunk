package box

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// claudeToken is the user's Claude token. "" means there's none. It's read
// from Home()/claude-token.cred, encrypted with systemd-creds under this
// machine's key (and TPM, if it has one), so the token isn't on disk in the
// clear. Store it with this, which reads the token from stdin:
//
//	systemd-creds encrypt --user --with-key=host+tpm2 --name=bunk-claude-token - ~/.local/share/bunk/claude-token.cred
//
// A machine without systemd-creds can use a plain Home()/claude-token, mode
// 0600, instead.
func claudeToken() (string, error) {
	enc := filepath.Join(Home(), "claude-token.cred")
	if _, err := os.Stat(enc); err == nil {
		cmd := exec.Command("systemd-creds", "decrypt", "--user", "--name=bunk-claude-token", enc, "-")
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", errors.New("decrypting the Claude token: " + strings.TrimSpace(errb.String()))
		}
		return strings.TrimSpace(out.String()), nil
	}
	raw, err := os.ReadFile(filepath.Join(Home(), "claude-token"))
	if os.IsNotExist(err) {
		return "", nil
	}
	return strings.TrimSpace(string(raw)), err
}
