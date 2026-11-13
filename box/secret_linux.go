package box

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On Linux, bunk's secrets live in Home()/NAME.cred, encrypted with
// systemd-creds under the machine's key (and TPM, if it has one), so they're
// not on disk in the clear. A person can store one with this, which reads it
// from stdin:
//
//	systemd-creds encrypt --user --with-key=host+tpm2 --name=bunk-NAME - ~/.local/share/bunk/NAME.cred
//
// A machine without systemd-creds can use a plain Home()/NAME, mode 0600.

// LoadSecret returns the secret called name, or nil if there's none.
func LoadSecret(name string) ([]byte, error) {
	enc := filepath.Join(Home(), name+".cred")
	if _, err := os.Stat(enc); err == nil {
		cmd := exec.Command("systemd-creds", "decrypt", "--user", "--name=bunk-"+name, enc, "-")
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("decrypting %s: %s", enc, strings.TrimSpace(errb.String()))
		}
		return out.Bytes(), nil
	}
	raw, err := os.ReadFile(filepath.Join(Home(), name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

// SaveSecret stores the secret called name, replacing any it had: encrypted,
// if systemd-creds is here, else in a mode-0600 file. The value goes to
// systemd-creds on its stdin.
func SaveSecret(name string, value []byte) error {
	if _, err := exec.LookPath("systemd-creds"); err != nil {
		return writeFileAtomic(filepath.Join(Home(), name), value)
	}
	enc := filepath.Join(Home(), name+".cred")
	tmp := enc + ".new"
	cmd := exec.Command("systemd-creds", "encrypt", "--user", "--with-key=host+tpm2", "--name=bunk-"+name, "-", tmp)
	cmd.Stdin = bytes.NewReader(value)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("encrypting %s: %s", enc, strings.TrimSpace(errb.String()))
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, enc)
}
