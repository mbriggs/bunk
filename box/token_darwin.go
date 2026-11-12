package box

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// keychainService names the Keychain item holding the token on a Mac. Store
// it with this, which asks for the token rather than taking it as an
// argument:
//
//	security add-generic-password -U -s bunk-claude-token -a "$USER" -w
const keychainService = "bunk-claude-token"

// claudeToken is the user's Claude token, from the login Keychain. "" means
// there's none.
func claudeToken() (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", keychainService, "-w")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 { // errSecItemNotFound
			return "", nil
		}
		return "", errors.New("reading the Claude token from the Keychain: " + strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
