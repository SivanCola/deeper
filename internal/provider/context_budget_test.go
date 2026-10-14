package provider

import (
	"context"
	"testing"
)

type unknownBudgetProvider struct{}

func (unknownBudgetProvider) Name() string { return "unknown" }
func (unknownBudgetProvider) Stream(context.Context, Request) (<-chan Chunk, error) {
	return nil, nil
}

type policyProvider struct {
	policy ContextBudgetPolicy
}

func (policyProvider) Name() string { return "policy" }
func (policyProvider) Stream(context.Context, Request) (<-chan Chunk, error) {
	return nil, nil
}
func (p policyProvider) ContextBudgetPolicy() ContextBudgetPolicy { return p.policy }

func TestResolveContextBudgetPolicyWithoutMetadata(t *testing.T) {
	got := ResolveContextBudgetPolicy(unknownBudgetProvider{})
	if got.WindowMode != ContextWindowUnknown || got.AutoOutputTokens != 0 || got.MaxOutputTokens != 0 || got.LimitMode != OutputLimitOmitWhenSafe {
		t.Fatalf("unknown policy = %+v", got)
	}
}

func TestResolveContextBudgetPolicyUsesDeclaredPolicy(t *testing.T) {
	want := ContextBudgetPolicy{
		WindowMode:       ContextWindowShared,
		AutoOutputTokens: DeepSeekMaxOutputTokens,
		MaxOutputTokens:  DeepSeekMaxOutputTokens,
		LimitMode:        OutputLimitOmitWhenSafe,
	}
	got := ResolveContextBudgetPolicy(policyProvider{policy: want})
	if got != want {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
}

func TestResolveContextBudgetPolicyNilProvider(t *testing.T) {
	if got := ResolveContextBudgetPolicy(nil); got.WindowMode != ContextWindowUnknown {
		t.Fatalf("nil policy = %+v", got)
	}
}
