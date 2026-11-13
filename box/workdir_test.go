package box

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// Boxes made at the same moment on a new machine all make bunk's key. They
// must end with one, whose public half is the private half's: a box that
// authorized any other would refuse every login.
func TestClientKeyIsOneKeyWhenMadeAtOnce(t *testing.T) {
	home := testHome(t)
	lines := make([][]byte, 16)
	var wg sync.WaitGroup
	for i := range lines {
		wg.Go(func() {
			line, err := clientKey()
			if err != nil {
				t.Error(err)
			}
			lines[i] = line
		})
	}
	wg.Wait()

	raw, err := os.ReadFile(filepath.Join(home, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := ssh.MarshalAuthorizedKey(signer.PublicKey())
	for i, line := range lines {
		if !bytes.Equal(line, want) {
			t.Errorf("caller %d got %q, want the key in place, %q", i, line, want)
		}
	}
	if pub, _ := os.ReadFile(filepath.Join(home, "id_ed25519.pub")); !bytes.Equal(pub, want) {
		t.Errorf("id_ed25519.pub is %q, want %q", pub, want)
	}
	if left, _ := filepath.Glob(filepath.Join(home, ".id_ed25519*")); len(left) > 0 {
		t.Errorf("scratch files left: %v", left)
	}
}
