package config

import "testing"

func TestDecodeRejectsOldInputsBeforeScopeMerge(t *testing.T) {
	for _, text := range []string{
		`[permissions]
allow=['Bash=echo hi']`,
		`[permissions]
deny=['Bash(git *)']`,
		`[permissions]
deny=['Set-Content']`,
		`[desktop]
layout_style='classic'`,
		`[[plugins]]
name='old'
type='sse'
url='https://example.test/sse'`,
	} {
		t.Run(text, func(t *testing.T) {
			cfg := Default()
			if _, err := decodeTOMLBytes([]byte(text), cfg); err == nil {
				t.Fatal("old input was accepted")
			}
		})
	}
}

func TestDecodeCurrentPermissionRules(t *testing.T) {
	cfg := Default()
	if _, err := decodeTOMLBytes([]byte(`[permissions]
allow=['Bash("rm *.log")','Bash(go test:*)']
deny=['Bash(Set-Content:*)']`), cfg); err != nil {
		t.Fatal(err)
	}
}

func TestProviderCatalogDoesNotInferPresetFromName(t *testing.T) {
	if _, _, ok := CatalogForProviderEntry(&ProviderEntry{Name: "deepseek-anthropic"}); ok {
		t.Fatal("preset inferred from old name")
	}
}
