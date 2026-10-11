package config

import (
	"strings"
	"testing"
)

func currentSelectionConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{ConfigVersion: CurrentConfigVersion, Providers: []ProviderEntry{{
		Name: "opencode-go", Kind: "openai", BaseURL: "https://opencode.ai/zen/go/v1",
		Models: []string{"deepseek-v4-flash"}, Default: "deepseek-v4-flash",
		APIKeyEnv: "OPENCODE_API_KEY",
	}}}
}

func TestOpenCodeGoExplicitSelectionIdentity(t *testing.T) {
	c := currentSelectionConfig(t)
	c.Providers[0].NoProxy = true
	ref := "opencode-go/deepseek-v4-flash"
	identity := c.ModelSelectionIdentity(ref)
	if len(identity) != 64 || strings.Contains(identity, "SELECTION_TEST_KEY") {
		t.Fatal("selection must contain only an identity digest")
	}
	if got, err := c.ResolveSavedModel(ref, identity); err != nil || got != ref {
		t.Fatalf("acknowledged selection failed: %q, %v", got, err)
	}
	c.Providers[0].Default = "deepseek-v4-pro"
	if _, err := c.ResolveSavedModel(ref, identity); err != nil {
		t.Fatalf("default edit changed explicit model identity: %v", err)
	}
	c.Providers[0].APIKeyEnv = "OTHER_KEY"
	if _, err := c.ResolveSavedModel(ref, identity); err == nil {
		t.Fatal("acknowledgement silently followed a later account edit")
	}
}
