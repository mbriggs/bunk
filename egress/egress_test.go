package egress

import (
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefusal(t *testing.T) {
	dir := t.TempDir()
	p := Policy{AllowFile: filepath.Join(dir, "allow.txt"), ModeFile: filepath.Join(dir, "mode")}
	os.WriteFile(p.AllowFile, []byte("pypi.org  # python\n.crates.io\n~[a-z0-9]+\\.ewr\\.prisma\\.build\n~(bad\ncodeload.github.com\n~.*\\.github\\.com\n"), 0o644)
	os.WriteFile(p.ModeFile, []byte("allowlist\n"), 0o644)
	cases := []struct {
		name  string
		allow bool
	}{
		{"pypi.org", true}, // trailing comment ignored
		{"PyPI.org.", true},
		{"evil-pypi.org", false},
		{"static.crates.io", true},
		{"crates.io", true},
		{"cmpbfbsdp09hr3jf7pojjs5qs.ewr.prisma.build", true},
		{"x.ewr.prisma.build.evil.com", false}, // anchored at the end
		{"evil.com.x.ewr.prisma.build", false}, // [a-z0-9]+ has no dots
		{"example.com", false},
		{"codeload.github.com", true},
		{"github.com", false},     // hard-refused even though a pattern matches it
		{"api.github.com", false}, // hard-refused, whatever the list says
		{"", false},
	}
	for _, c := range cases {
		if got := p.Refusal(c.name) == ""; got != c.allow {
			t.Errorf("Refusal(%q) allowed=%v, want %v", c.name, got, c.allow)
		}
	}
}

func TestOpenModeStillGuardsGitHub(t *testing.T) {
	dir := t.TempDir()
	p := Policy{AllowFile: filepath.Join(dir, "allow.txt"), ModeFile: filepath.Join(dir, "mode")}
	os.WriteFile(p.ModeFile, []byte("open\n"), 0o644)
	for name, allow := range map[string]bool{
		"example.com": true, "github.com": false, "codeload.github.com": false,
		"raw.githubusercontent.com": false,
	} {
		if got := p.Refusal(name) == ""; got != allow {
			t.Errorf("open: Refusal(%q) allowed=%v, want %v", name, got, allow)
		}
	}
}

// The resolver hands a literal address back IPv4-mapped, and a name's
// addresses plain. Either way, only a public one is dialed.
func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"104.20.23.154": true, "::ffff:104.20.23.154": true,
		"100.100.100.100": false, "::ffff:100.100.100.100": false,
		"10.0.0.5": false, "::ffff:10.0.0.5": false,
		"127.0.0.1": false, "::ffff:127.0.0.1": false,
		"169.254.169.254": false, "::ffff:169.254.169.254": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("public(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestPaths(t *testing.T) {
	dir := t.TempDir()
	p := Policy{AllowFile: filepath.Join(dir, "allow.txt"), ModeFile: filepath.Join(dir, "mode")}
	os.WriteFile(p.AllowFile, []byte("Storage.googleapis.com/chrome-for-testing-public  # CfT\nexample.com/a/\ngithub.com/x/\n"), 0o644)
	if p.Refusal("storage.googleapis.com") == "" {
		t.Error("a path entry allowed its whole host")
	}
	if got := p.Paths("storage.googleapis.com"); len(got) != 1 || got[0] != "/chrome-for-testing-public/" {
		t.Errorf("Paths = %q", got)
	}
	if got := p.Paths("github.com"); got != nil {
		t.Errorf("Paths(github.com) = %q, want none", got)
	}
	paths := []string{"/chrome-for-testing-public/"}
	for _, c := range []struct {
		method, target string
		body           string
		allow          bool
	}{
		{"GET", "/chrome-for-testing-public/158/linux64/chrome-linux64.zip", "", true},
		{"HEAD", "/chrome-for-testing-public/158/x.zip?alt=media", "", true},
		{"PUT", "/chrome-for-testing-public/x", "", false},
		{"POST", "/upload/storage/v1/b/evil/o", "", false},
		{"GET", "/chrome-for-testing-public", "", false},
		{"GET", "/chrome-for-testing-public-evil/x", "", false},
		{"GET", "/evil-bucket/secret", "", false},
		{"GET", "/chrome-for-testing-public/../evil/x", "", false},
		{"GET", "/chrome-for-testing-public/%2e%2e/evil/x", "", false},
		{"GET", "/chrome-for-testing-public%2F..%2Fevil/x", "", false},
		{"GET", "/chrome-for-testing-public/x", "data", false},
	} {
		req := httptest.NewRequest(c.method, "https://storage.googleapis.com"+c.target, strings.NewReader(c.body))
		if got := pathRefusal(req, paths) == ""; got != c.allow {
			t.Errorf("%s %s allowed=%v, want %v", c.method, c.target, got, c.allow)
		}
	}
}
