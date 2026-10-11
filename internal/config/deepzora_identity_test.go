package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeepzoraDoesNotLoadReasonixState(t *testing.T) {
	home := t.TempDir()
	original := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = original })
	for _, key := range []string{"DEEPZORA_HOME", "DEEPZORA_STATE_HOME", "DEEPZORA_CACHE_HOME"} {
		t.Setenv(key, "")
	}
	t.Setenv("REASONIX_HOME", filepath.Join(home, ".reasonix"))
	t.Setenv("REASONIX_STATE_HOME", filepath.Join(home, ".reasonix-state"))
	t.Setenv("REASONIX_CACHE_HOME", filepath.Join(home, ".reasonix-cache"))
	upstream := filepath.Join(home, ".reasonix")
	if err := os.MkdirAll(upstream, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upstream, "config.toml"), []byte("default_model = 'upstream-private-model'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{UserConfigPath(), MemoryUserDir(), CacheDir(), userConfigLoadPath()} {
		if strings.Contains(dir, "reasonix") || !strings.Contains(dir, "deepzora") {
			t.Fatalf("Deepzora path %q touches another product's state", dir)
		}
	}
	if IsolatedHomeDir() != "" {
		t.Fatal("REASONIX_HOME must not configure Deepzora")
	}
}
