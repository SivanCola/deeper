package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/config"
)

func TestDoctorPreservesProjectSandboxHintAndDeclarationValues(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	t.Chdir(root)
	project := "[sandbox]\nnetwork=false\n[permissions]\nallow=['Bash=echo synthetic-rule']\n"
	if err := os.WriteFile(filepath.Join(root, "deepzora.toml"), []byte(project), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadForRootReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HasLoadWarnings() {
		t.Fatalf("ordinary declaration produced a banner warning: %v", cfg.LoadWarnings())
	}
	text := RenderText(Collect(Options{Config: cfg}))
	for _, want := range []string{"sets [sandbox]", "narrow user-level Settings", "permissions.allow", "Bash=echo synthetic-rule", "approval is required"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}

func TestDoctorPreservesUnknownPresetKeyValueAndProjectOrigin(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "deepzora.toml"), []byte("[desktop]\ndefault_tool_approval_mode='unknown-project-preset'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadForRootReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	text := RenderText(Collect(Options{Config: cfg}))
	if !strings.Contains(text, "project config sets desktop.default_tool_approval_mode") || !strings.Contains(text, "unknown-project-preset") {
		t.Fatalf("warning lost project origin or key/value: %s", text)
	}
}

func TestDoctorRedactsCredentialsInIgnoredProjectDeclaration(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	const secret = "synthetic-key-9876543210"
	project := "[permissions]\nallow=['Bash=echo api_key=" + secret + "']\n"
	if err := os.WriteFile(filepath.Join(root, "deepzora.toml"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadForRootReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	report := Collect(Options{Config: cfg})
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{RenderText(report), string(raw)} {
		if strings.Contains(rendered, secret) || !strings.Contains(rendered, "permissions.allow") {
			t.Fatalf("diagnostic must preserve declaration context without its credential: %s", rendered)
		}
	}
}
