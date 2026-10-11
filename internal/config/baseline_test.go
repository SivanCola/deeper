package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineRejectsHistoricalConfigWithoutChangingSource(t *testing.T) {
	for _, raw := range []string{
		"config_version = 11\n",
		"[bot.allowlist]\nallow_all = true\n",
		"[desktop]\nstatus_bar_style_initialized = true\n",
		"[[providers]]\nname = \"old\"\nchat_url = \"https://example.test\"\n",
		"[agent]\nmax_steps = 20\n",
		"[agent]\nauto_plan = \"off\"\n",
		"[agent]\ncompletion_validation = \"off\"\n",
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadForEditReadOnlyStrict(path); err == nil {
			t.Fatalf("accepted historical config: %s", raw)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != raw {
			t.Fatal("rejected config was changed")
		}
	}
}

func TestBaselineSaveReloadPreservesCurrentPreferences(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	c := Default()
	if err := c.SetDesktopDefaultToolApprovalMode("read-only"); err != nil {
		t.Fatal(err)
	}
	path := UserConfigPath()
	if err := c.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		loaded, err := LoadForEditReadOnlyStrict(path)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.ConfigVersion != CurrentConfigVersion || loaded.DesktopDefaultToolApprovalMode() != "read-only" {
			t.Fatal("current preferences changed")
		}
		if err := loaded.SaveTo(path); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"auto_plan =", "recent_keep =", "credentials_store =", "plan_mode_read_only_commands =", "[bot.allowlist]", "status_bar_style_initialized"} {
		if strings.Contains(string(b), key) {
			t.Fatalf("wrote retired setting %s", key)
		}
	}
}
