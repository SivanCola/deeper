package config

import (
	"strings"
	"testing"
)

// TestProviderConfigured verifies Configured tracks whether the provider can be
// selected. Providers with no api_key_env are explicit no-auth providers; if an
// env var is configured, it must resolve to a non-empty value.
func TestProviderConfigured(t *testing.T) {
	cases := []struct {
		name string
		p    ProviderEntry
		want bool
	}{
		{"key set", ProviderEntry{APIKeyEnv: "DEEPZORA_TEST_KEY", resolvedAPIKey: "secret"}, true},
		{"key env empty", ProviderEntry{APIKeyEnv: "DEEPZORA_TEST_EMPTY"}, false},
		{"key env unset", ProviderEntry{APIKeyEnv: "DEEPZORA_TEST_MISSING"}, false},
		{"loopback key env unset", ProviderEntry{BaseURL: "http://127.0.0.1:23333/v1", APIKeyEnv: "DEEPZORA_TEST_MISSING"}, true},
		{"official endpoint without key env", ProviderEntry{BaseURL: "https://api.deepseek.com"}, false},
		{"no api_key_env", ProviderEntry{}, true},
	}
	for _, c := range cases {
		if got := c.p.Configured(); got != c.want {
			t.Errorf("%s: Configured() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidateAllowsNoAuthProvider(t *testing.T) {
	c := &Config{
		DefaultModel: "local/model-a",
		Providers: []ProviderEntry{{
			Name:    "local",
			Kind:    "openai",
			BaseURL: "http://127.0.0.1:23333/v1",
			Models:  []string{"model-a", "model-b"},
			Default: "model-a",
		}},
	}

	if err := c.Validate("local/model-b"); err != nil {
		t.Fatalf("Validate no-auth local provider: %v", err)
	}

	c.Providers[0].APIKeyEnv = "LOCAL_API_KEY"
	if err := c.Validate("local/model-b"); err != nil {
		t.Fatalf("Validate loopback local provider with missing key env: %v", err)
	}
}

func TestValidateExplainsInvalidCredentialVariableName(t *testing.T) {
	c := &Config{
		Providers: []ProviderEntry{{
			Name:      "relay",
			Kind:      "openai",
			BaseURL:   "https://api.example.com/v1",
			Model:     "grok-4.5",
			APIKeyEnv: "grok-4.5",
		}},
	}
	err := c.Validate("relay")
	if err == nil {
		t.Fatal("Validate should reject an invalid api_key_env")
	}
	if got := err.Error(); !strings.Contains(got, "api_key_env is invalid") || !strings.Contains(got, "not a model name") {
		t.Fatalf("Validate error = %q, want actionable credential variable guidance", got)
	}
}

func TestValidateDoesNotEchoCredentialPastedAsVariableName(t *testing.T) {
	const secret = "sk-project-test-only-secret-123456789"
	c := &Config{Providers: []ProviderEntry{{Name: "relay", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "chat", APIKeyEnv: secret}}}
	err := c.Validate("relay")
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "api_key_env is invalid") {
		t.Fatalf("credential-name validation must retain guidance without echoing the supplied value: %v", err)
	}
}
