package box

import (
	"archive/tar"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// writeIdentity makes the box's identity drive: a raw tar archive, attached as
// a second disk, holding its hostname, a freshly generated SSH host key, and
// the keys that may log in (Home()/authorized_keys, so the image carries no
// one's keys). The guest's punchclock-identity unit unpacks it before sshd
// starts. A tar needs no filesystem tools on the host, and its 512-byte
// records are whole disk sectors. The public key is also kept beside it, for known_hosts.
func writeIdentity(b Box) error {
	authorized, err := os.ReadFile(filepath.Join(Home(), "authorized_keys"))
	if err != nil {
		return fmt.Errorf("the keys that may log in to boxes: %w", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(priv, "punchclock "+b.Name)
	if err != nil {
		return err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	pubLine := ssh.MarshalAuthorizedKey(sshPub)

	f, err := os.OpenFile(b.path("identity.tar"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	for _, file := range []struct {
		name string
		mode int64
		data []byte
	}{
		{"hostname", 0o644, []byte(b.Name + "\n")},
		{"ssh_host_ed25519_key", 0o600, pem.EncodeToMemory(block)},
		{"ssh_host_ed25519_key.pub", 0o644, pubLine},
		{"authorized_keys", 0o600, authorized},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.data))}); err != nil {
			return err
		}
		if _, err := tw.Write(file.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.WriteFile(b.path("host_key.pub"), pubLine, 0o644)
}
