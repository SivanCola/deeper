package anthropic

import (
	"testing"

	"deepzora/internal/provider"
)

func TestNativeAnthropicBudgetPolicy(t *testing.T) {
	c := &client{defaultMaxTokens: 16 * 1024}
	if got := c.ContextBudgetPolicy(); got.WindowMode != provider.ContextWindowUnknown || got.AutoOutputTokens != 0 || got.LimitMode != provider.OutputLimitRequired {
		t.Fatalf("native Anthropic policy = %+v", got)
	}
}

func TestOfficialDeepSeekAnthropicPolicyRequires384K(t *testing.T) {
	p, err := New(provider.Config{Name: "ds", BaseURL: "https://api.deepseek.com/anthropic", Model: "deepseek-v4-flash"})
	if err != nil {
		t.Fatal(err)
	}
	got := p.(provider.ContextBudgetPolicyProvider).ContextBudgetPolicy()
	if got.LimitMode != provider.OutputLimitRequired || got.AutoOutputTokens != provider.DeepSeekMaxOutputTokens {
		t.Fatalf("DeepSeek Anthropic policy = %+v", got)
	}
}

func TestOpenCodeGoAnthropicPolicyUsesTable(t *testing.T) {
	p, err := New(provider.Config{Name: "og", BaseURL: "https://opencode.ai/zen/go", Model: "qwen3.7-plus"})
	if err != nil {
		t.Fatal(err)
	}
	got := p.(provider.ContextBudgetPolicyProvider).ContextBudgetPolicy()
	if got.WindowMode != provider.ContextWindowShared || got.MaxOutputTokens != 65_536 || got.LimitMode != provider.OutputLimitRequired {
		t.Fatalf("OpenCode Go Anthropic policy = %+v", got)
	}
}
