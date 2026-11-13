package box

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Codex in a box signs in with a ChatGPT login of bunk's own, the secret
// "codex-auth": Codex's auth.json, made by CodexLogin. It's apart from the
// host's own Codex logins on purpose. Codex rotates its refresh token, so a
// box refreshing a login the host also used would sign the host out. bunk
// run copies it into each box, and keeps the box's copy afterwards if Codex
// there refreshed it (see task.Run).

// CodexLogin signs bunk's Codex in with a device code: it prints a link and a
// code to enter in a browser anywhere. It runs `codex login` in a scratch box,
// so the host needs no Codex of its own, and stores the login it makes.
func CodexLogin() error {
	b, err := Open("codex-login")
	if err != nil {
		return err
	}
	if b.Exists() {
		if err := b.Remove(); err != nil {
			return err
		}
	}
	if err := b.Create(""); err != nil {
		return err
	}
	defer b.Remove()
	if err := b.Up(); err != nil {
		return err
	}
	cfg, err := b.SSHConfig()
	if err != nil {
		return err
	}
	login := exec.Command("ssh", "-t", "-F", cfg, b.Name,
		`export CODEX_HOME=$(mktemp -d) && codex login --device-auth && cp "$CODEX_HOME/auth.json" ~/.codex-login.json`)
	login.Stdin, login.Stdout, login.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := login.Run(); err != nil {
		return fmt.Errorf("codex login: %w", err)
	}
	read := exec.Command("ssh", "-F", cfg, b.Name, "cat ~/.codex-login.json")
	var out, errb bytes.Buffer
	read.Stdout, read.Stderr = &out, &errb
	if err := read.Run(); err != nil {
		return fmt.Errorf("reading the login: %v %s", err, strings.TrimSpace(errb.String()))
	}
	if _, err := CodexRefreshed(out.Bytes()); err != nil {
		return err
	}
	return SaveSecret("codex-auth", out.Bytes())
}

// CodexRefreshed returns when Codex last refreshed the login in auth.json,
// as it records it, or an error if auth.json isn't a ChatGPT login.
func CodexRefreshed(auth []byte) (string, error) {
	var a struct {
		AuthMode    string          `json:"auth_mode"`
		LastRefresh string          `json:"last_refresh"`
		Tokens      json.RawMessage `json:"tokens"`
	}
	if err := json.Unmarshal(auth, &a); err != nil {
		return "", fmt.Errorf("Codex's auth.json: %w", err)
	}
	if a.AuthMode != "chatgpt" || len(a.Tokens) == 0 {
		return "", errors.New("Codex's auth.json isn't a ChatGPT login")
	}
	return a.LastRefresh, nil
}
