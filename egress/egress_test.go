package egress

import (
	"os"
	"path/filepath"
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
