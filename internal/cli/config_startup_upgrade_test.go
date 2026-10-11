package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/config"
)

func TestRunAppliesUserConfigUpgradesOnStartup(t *testing.T) {
	isolateCLIConfigHome(t)
	path := config.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("config_version = 12\ndefault_model = \"deepseek-flash\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() {
		if rc := Run([]string{"mcp", "list"}, "test-version"); rc != 0 {
			t.Fatalf("mcp list rc = %d, want 0", rc)
		}
	})

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgraded user config: %v", err)
	}
	if !strings.Contains(string(body), fmt.Sprintf("config_version = %d", config.Default().ConfigVersion)) {
		t.Fatalf("CLI startup should apply user config upgrades:\n%s", body)
	}
}
