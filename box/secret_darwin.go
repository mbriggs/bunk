package box

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On a Mac, bunk's secrets live in the login Keychain, as generic passwords
// named bunk-NAME. A person can store one with this, which asks for it rather
// than taking it as an argument:
//
//	security add-generic-password -U -s bunk-NAME -a "$USER" -w

// LoadSecret returns the secret called name, or nil if there's none: from
// Home()/NAME.enc if SaveSecret sealed it there, else from the Keychain.
func LoadSecret(name string) ([]byte, error) {
	if sealed, err := os.ReadFile(filepath.Join(Home(), name+".enc")); err == nil {
		key, err := keychainGet("bunk-secrets-key")
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("unsealing %s: no key in the Keychain (%v)", name, err)
		}
		return open(key, sealed)
	}
	return keychainGet("bunk-" + name)
}

// keychainGet returns the generic password service, or nil if there's none.
// `security -w` prints a value with a newline or other unprintable byte in
// hex, and a printable one as is, without saying which; `-g` says, so its
// report is parsed instead.
func keychainGet(service string) ([]byte, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", service, "-g")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 { // errSecItemNotFound
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s from the Keychain: %s", service, strings.TrimSpace(errb.String()))
	}
	for _, line := range strings.Split(errb.String(), "\n") {
		v, ok := strings.CutPrefix(line, "password: ")
		if !ok {
			continue
		}
		if h, ok := strings.CutPrefix(v, "0x"); ok {
			h, _, _ = strings.Cut(h, " ")
			return hex.DecodeString(h)
		}
		if strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
			return []byte(v[1 : len(v)-1]), nil
		}
		return []byte{}, nil // an empty password
	}
	return nil, fmt.Errorf("reading %s from the Keychain: no password in its report", service)
}

// SaveSecret stores the secret called name, replacing any it had. A short
// one goes in the Keychain itself. `security -i`, which takes the value on
// stdin so it's in no command line, reads a line at a time and can't take a
// long one, such as Codex's login. So a long one is encrypted (AES-256-GCM)
// with a random key kept in the Keychain as bunk-secrets-key, and written to
// Home()/NAME.enc: the Keychain still holds what unlocks it.
func SaveSecret(name string, value []byte) error {
	if len(value) <= 512 {
		os.Remove(filepath.Join(Home(), name+".enc"))
		return keychainAdd("bunk-"+name, value)
	}
	key, err := secretsKey()
	if err != nil {
		return err
	}
	sealed, err := seal(key, value)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(Home(), name+".enc"), sealed); err != nil {
		return err
	}
	exec.Command("security", "delete-generic-password", "-s", "bunk-"+name).Run() // a short value it replaces
	return nil
}

// keychainAdd stores value as the generic password service, as hex on
// security's stdin.
func keychainAdd(service string, value []byte) error {
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -X %s\n", service, os.Getenv("USER"), hex.EncodeToString(value)))
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &errb, &errb
	if err := cmd.Run(); err != nil || errb.Len() > 0 {
		return fmt.Errorf("storing %s in the Keychain: %v %s", service, err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// secretsKey is the key that encrypts bunk's long secrets, made on first use.
func secretsKey() ([]byte, error) {
	if k, err := keychainGet("bunk-secrets-key"); err != nil || len(k) == 32 {
		return k, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, keychainAdd("bunk-secrets-key", k)
}

func seal(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func open(key, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("the sealed secret is too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
}
