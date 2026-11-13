package box

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// A box made for a directory (bunk run) records it in its workdir file. That
// is how `bunk rm --dir` finds the box, and how gc knows a box whose
// directory is gone has nothing left to do.

var notNameChar = regexp.MustCompile(`[^a-z0-9]+`)

// ForDir returns the box for directory dir, which may not exist yet: the
// directory's name, cut to fit, and a hash of its absolute path, so two
// directories never share a box and the same one always gets its own back.
func ForDir(dir string) (Box, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Box{}, err
	}
	sum := sha256.Sum256([]byte(abs))
	hash := hex.EncodeToString(sum[:3])
	n := strings.Trim(notNameChar.ReplaceAllString(strings.ToLower(filepath.Base(abs)), "-"), "-")
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		n = "d-" + n
	}
	if len(n) > 31-len(hash) {
		n = strings.TrimRight(n[:31-len(hash)], "-")
	}
	return Open(n + "-" + hash)
}

// Workdir is the directory the box was made for, or "" for a box made by
// name (bunk up).
func (b Box) Workdir() string {
	raw, _ := os.ReadFile(b.path("workdir"))
	return strings.TrimSpace(string(raw))
}

// clientKey returns the public half of the key bunk itself logs in to boxes
// with, Home()/id_ed25519, making it on first use. Every box authorizes it,
// beside Home()/authorized_keys.
func clientKey() ([]byte, error) {
	path := filepath.Join(Home(), "id_ed25519")
	if raw, err := os.ReadFile(path + ".pub"); err == nil {
		return raw, nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "bunk")
	if err != nil {
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	line := ssh.MarshalAuthorizedKey(sshPub)
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return line, os.WriteFile(path+".pub", line, 0o644)
}

// SSHConfig writes an ssh config that reaches the box as host NAME, through
// this binary's `proxy`, with bunk's own key and the box's pinned host key,
// and returns its path. Connections share one ssh session, so bunk run's
// copies and polls don't each pay for a handshake.
func (b Box) SSHConfig() (string, error) {
	key, err := b.HostKey()
	if err != nil {
		return "", err
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	user, err := os.ReadFile(b.path("user"))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(b.path("known_hosts"), []byte(b.Name+" "+key+"\n"), 0o600); err != nil {
		return "", err
	}
	cfg := fmt.Sprintf(`Host %s
  User %s
  IdentityFile %s
  IdentitiesOnly yes
  IdentityAgent none
  UserKnownHostsFile %s
  StrictHostKeyChecking yes
  ProxyCommand env %s=proxy %s proxy %s
  LogLevel ERROR
  ControlMaster auto
  ControlPath %s
  ControlPersist 30
`, b.Name, strings.TrimSpace(string(user)), filepath.Join(Home(), "id_ed25519"), b.path("known_hosts"), helperEnv, self, b.Name, b.path("ssh.ctl"))
	path := b.path("ssh_config")
	return path, os.WriteFile(path, []byte(cfg), 0o600)
}
