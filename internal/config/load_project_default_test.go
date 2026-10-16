package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProjectDefaultTestConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// An explicit project model is authoritative even if it cannot be resolved.
// Boot validation must report that value instead of silently selecting another.
func TestLoadForRoot_UnresolvableProjectDefaultModelRemainsExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	writeProjectDefaultTestConfig(t, home, "config.toml", `default_model = "deepseek-pro"

[[providers]]
name        = "deepseek-pro"
kind        = "openai"
base_url    = "https://api.deepseek.com"
models = ["deepseek-v4-pro"]
api_key_env = "DEEPSEEK_API_KEY"
`)

	project := t.TempDir()
	writeProjectDefaultTestConfig(t, project, "deepzora.toml", `default_model = "deepseek-flash"

[permissions]
allow = ["Bash(go test*)"]
`)

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if cfg.DefaultModel != "deepseek-flash" {
		t.Fatalf("DefaultModel = %q, want explicit project value", cfg.DefaultModel)
	}
	if _, ok := cfg.ResolveModel(cfg.DefaultModel); ok {
		t.Fatalf("ResolveModel(%q) unexpectedly succeeded", cfg.DefaultModel)
	}
}

// A project default_model that does resolve is a legitimate override and must
// keep winning over the user config.
func TestLoadForRoot_ResolvableProjectDefaultModelStillWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	writeProjectDefaultTestConfig(t, home, "config.toml", `default_model = "deepseek-pro"

[[providers]]
name        = "deepseek-pro"
kind        = "openai"
base_url    = "https://api.deepseek.com"
models = ["deepseek-v4-pro"]
api_key_env = "DEEPSEEK_API_KEY"

[[providers]]
name        = "local"
kind        = "openai"
base_url    = "http://127.0.0.1:8080/v1"
models = ["local-model"]
`)

	project := t.TempDir()
	writeProjectDefaultTestConfig(t, project, "deepzora.toml", `default_model = "local"
`)

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if cfg.DefaultModel != "local" {
		t.Fatalf("DefaultModel = %q, want project override %q kept", cfg.DefaultModel, "local")
	}
}

// When neither the project override nor the user default resolves, keep the
// project value so the existing boot error names what the user actually wrote.
func TestLoadForRoot_ProjectDefaultModelKeptWhenUserDefaultUnresolvable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	writeProjectDefaultTestConfig(t, home, "config.toml", `default_model = "gone-too"

[[providers]]
name        = "deepseek-pro"
kind        = "openai"
base_url    = "https://api.deepseek.com"
models = ["deepseek-v4-pro"]
api_key_env = "DEEPSEEK_API_KEY"
`)

	project := t.TempDir()
	writeProjectDefaultTestConfig(t, project, "deepzora.toml", `default_model = "gone"
`)

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if cfg.DefaultModel != "gone" {
		t.Fatalf("DefaultModel = %q, want project value %q kept", cfg.DefaultModel, "gone")
	}
}

// When the project file is the user's only config, a broken default_model must
// keep failing loudly at boot instead of silently substituting the built-in
// default (see boot.TestBuildUnknownModelErrorIsActionable).
func TestLoadForRoot_NoUserConfigKeepsBrokenProjectDefaultModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)

	project := t.TempDir()
	writeProjectDefaultTestConfig(t, project, "deepzora.toml", `default_model = "legacy-missing"

[[providers]]
name        = "deepseek-flash"
kind        = "openai"
base_url    = "https://example.invalid"
models = ["deepseek-v4-flash"]
api_key_env = "DEEPZORA_TEST_KEY_UNSET"
`)

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if cfg.DefaultModel != "legacy-missing" {
		t.Fatalf("DefaultModel = %q, want broken project value %q kept", cfg.DefaultModel, "legacy-missing")
	}
}

// No project override: the user default is untouched.
func TestLoadForRoot_NoProjectOverrideLeavesUserDefaultAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	writeProjectDefaultTestConfig(t, home, "config.toml", `default_model = "deepseek-pro"

[[providers]]
name        = "deepseek-pro"
kind        = "openai"
base_url    = "https://api.deepseek.com"
models = ["deepseek-v4-pro"]
api_key_env = "DEEPSEEK_API_KEY"
`)

	project := t.TempDir()

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if cfg.DefaultModel != "deepseek-pro" {
		t.Fatalf("DefaultModel = %q, want %q", cfg.DefaultModel, "deepseek-pro")
	}
}
