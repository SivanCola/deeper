package responses

import (
	"testing"

	"deepzora/internal/provider"
)

func TestBudgetAndReplayPolicies(t *testing.T) {
	deepseek := &client{vendor: "deepseek", maxOutputTokens: 32 * 1024}
	if got := deepseek.ContextBudgetPolicy(); got.WindowMode != provider.ContextWindowShared || got.AutoOutputTokens != provider.DeepSeekMaxOutputTokens {
		t.Fatalf("DeepSeek policy = %+v", got)
	}
	policy := deepseek.SharedWindowInputPolicy()
	if !policy.ReplaysOrdinaryReasoning || !policy.ReplaysResponsesItems {
		t.Fatalf("DeepSeek input policy = %+v, want all Responses replay fields", policy)
	}
	mimo := &client{vendor: "mimo", maxOutputTokens: 128000}
	if got := mimo.ContextBudgetPolicy(); got.WindowMode != provider.ContextWindowUnknown || got.AutoOutputTokens != 0 {
		t.Fatalf("MiMo policy = %+v", got)
	}
	if policy := mimo.SharedWindowInputPolicy(); policy.ReplaysOrdinaryReasoning || policy.ReplaysResponsesItems {
		t.Fatalf("MiMo input policy = %+v, want no DeepSeek replay fields", policy)
	}
}

func TestOfficialDeepSeekResponsesPolicyOmitsWhenSafe(t *testing.T) {
	c := New(Config{Name: "ds", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro"}).(*client)
	got := c.ContextBudgetPolicy()
	if got.LimitMode != provider.OutputLimitOmitWhenSafe || got.AutoOutputTokens != provider.DeepSeekMaxOutputTokens {
		t.Fatalf("responses DeepSeek policy = %+v", got)
	}
	body, _, _ := c.buildRequestBody(provider.Request{})
	if _, exists := body["max_output_tokens"]; exists {
		t.Fatalf("safe official Responses should omit max_output_tokens: %#v", body["max_output_tokens"])
	}
	clipped, _, _ := c.buildRequestBody(provider.Request{MaxTokens: 229_502})
	if clipped["max_output_tokens"] != 229_502 {
		t.Fatalf("clipped Responses max_output_tokens = %#v", clipped["max_output_tokens"])
	}
}

func TestOpenCodeGoResponsesPolicySendsMaxOutputTokens(t *testing.T) {
	c := New(Config{Name: "og", BaseURL: "https://opencode.ai/zen/go/v1", Model: "deepseek-v4-flash"}).(*client)
	got := c.ContextBudgetPolicy()
	if got.LimitMode != provider.OutputLimitAlways || got.MaxOutputTokens != 384_000 {
		t.Fatalf("OpenCode Go Responses policy = %+v", got)
	}
	body, _, _ := c.buildRequestBody(provider.Request{MaxTokens: 384_000})
	if body["max_output_tokens"] != 384_000 {
		t.Fatalf("OpenCode Go Responses must send max_output_tokens, got %#v", body["max_output_tokens"])
	}
}
