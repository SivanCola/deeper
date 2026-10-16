package agent

import (
	"testing"

	"deepzora/internal/provider"
)

func TestCoveredPrefixHashIgnoresLocalToolRawContent(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read", Arguments: `{}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read", Content: "bounded", RawContent: "full result A"},
	}
	first := coveredPrefixHash(msgs, len(msgs))
	msgs[1].RawContent = "full result B"
	if second := coveredPrefixHash(msgs, len(msgs)); second != first {
		t.Fatal("local RawContent edit changed the provider-visible covered-prefix hash")
	}
	msgs[1].Content = "different bounded result"
	if second := coveredPrefixHash(msgs, len(msgs)); second == first {
		t.Fatal("provider-visible Content edit did not invalidate the covered-prefix hash")
	}
}
