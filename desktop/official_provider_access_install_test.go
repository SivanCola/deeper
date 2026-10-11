package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/config"
)

func TestAddOfficialProviderAccessUsesDesktopLanguagePricing(t *testing.T) {
	// Desktop language is display-only; newly installed official DeepSeek
	// templates freeze the default USD list-price table (billing_currency).
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
[desktop]
language = "zh"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := NewApp().AddOfficialProviderAccess("deepseek", ""); err != nil {
		t.Fatalf("AddOfficialProviderAccess: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	p, ok := cfg.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider not saved")
	}
	flash := p.Prices["deepseek-v4-flash"]
	pro := p.Prices["deepseek-v4-pro"]
	if flash == nil || flash.Output != 1.2 || flash.Currency != "$" {
		t.Fatalf("flash price = %+v, want frozen USD official table", flash)
	}
	if pro == nil || pro.Output != 3.96 || pro.Currency != "$" {
		t.Fatalf("pro price = %+v, want frozen USD official table", pro)
	}
	if got := cfg.ExplicitDisplayCurrency(); got != "" {
		t.Fatalf("display currency = %q, want unresolved auto", got)
	}
}

func TestAddOfficialProviderAccessPreservesExistingOfficialCustomization(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com/v1"
models = ["deepseek-v4-flash"]
default = "deepseek-v4-flash"
api_key_env = "MY_DEEPSEEK_KEY"
headers = { X-Route = "official-custom" }
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := NewApp().AddOfficialProviderAccess("deepseek", ""); err != nil {
		t.Fatalf("AddOfficialProviderAccess: %v", err)
	}
	cfg := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	p, ok := cfg.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider not saved")
	}
	if p.Kind != "openai" || p.BaseURL != "https://api.deepseek.com/v1" || p.APIKeyEnv != "MY_DEEPSEEK_KEY" || p.Headers["X-Route"] != "official-custom" {
		t.Fatalf("existing official customization was overwritten: %+v", p)
	}
	if !providerAccessSet(cfg.Desktop.ProviderAccess)["deepseek"] {
		t.Fatalf("provider_access missing preserved DeepSeek provider: %+v", cfg.Desktop.ProviderAccess)
	}
}

func TestAddOfficialProviderAccessRejectsSameNameCustomEndpointBeforeSavingKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://proxy.example.invalid/v1"
models = ["deepseek-v4-flash"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := NewApp().AddOfficialProviderAccess("deepseek", "test-key")
	if err == nil || !strings.Contains(err.Error(), "already belongs to a custom endpoint") {
		t.Fatalf("AddOfficialProviderAccess error = %v, want same-name custom endpoint rejection", err)
	}
	if data, readErr := os.ReadFile(config.UserCredentialsPath()); readErr == nil && strings.Contains(string(data), "DEEPSEEK_API_KEY") {
		t.Fatalf("credential was saved before provider name conflict rejection:\n%s", data)
	}
	cfg := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	p, ok := cfg.Provider("deepseek")
	if !ok || p.BaseURL != "https://proxy.example.invalid/v1" {
		t.Fatalf("same-name custom provider changed after rejection: %+v", p)
	}
}
