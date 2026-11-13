package task

import "testing"

// A directory the host's Codex already trusts has its table in the config
// copied in. Naming the table again would make the box's config.toml a TOML
// error.
func TestProjectTrustSkipsDirectoriesTheConfigHas(t *testing.T) {
	cfg := "model = \"x\"\n\n[projects.\"/src/app\"]\ntrust_level = \"trusted\"\n"
	got := projectTrust(cfg, []string{"/src/app", "/src/app/sub"})
	want := "\n[projects.\"/src/app/sub\"]\ntrust_level = \"trusted\"\n"
	if got != want {
		t.Errorf("projectTrust = %q, want %q", got, want)
	}
}
