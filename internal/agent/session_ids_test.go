package agent

import (
	"strings"
	"testing"

	"deepzora/internal/provider"
)

func idFixtureMessages() []provider.Message {
	return []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi", CreatedAt: 1},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "ls", Arguments: `{}`}}},
		{Role: provider.RoleTool, Content: "main.go", ToolCallID: "call_1", Name: "ls", ToolExecution: &provider.ToolExecution{}},
		{Role: provider.RoleAssistant, Content: "done", ReasoningContent: "r"},
	}
}

func withIDs(msgs []provider.Message) []provider.Message {
	out := append([]provider.Message(nil), msgs...)
	for i := range out {
		out[i].ID = NewMessageID()
	}
	return out
}

func TestNewMessageIDShapeAndUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 2000)
	prev := ""
	for range 2000 {
		id := NewMessageID()
		if len(id) != messageIDLen {
			t.Fatalf("len(%q) = %d, want %d", id, len(id), messageIDLen)
		}
		for _, r := range id {
			if !strings.ContainsRune(messageIDAlphabet, r) {
				t.Fatalf("id %q contains %q outside the alphabet", id, r)
			}
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = struct{}{}
		if prev != "" && id[:10] < prev[:10] {
			t.Fatalf("time prefix went backwards: %q after %q", id, prev)
		}
		prev = id
	}
}

func TestCurrentStorePreservesMessageIDsAcrossReopen(t *testing.T) {
	state := newCurrentPersistenceSession(t, &Session{Messages: withIDs(idFixtureMessages())})
	first := state.reopen(t, state.ref)
	second := state.reopen(t, state.ref)
	if len(first.Messages) != len(second.Messages) || len(first.Messages) == 0 {
		t.Fatal("reopen changed message count")
	}
	for i := range first.Messages {
		if first.Messages[i].ID == "" || first.Messages[i].ID != second.Messages[i].ID {
			t.Fatalf("message %d identity changed", i)
		}
	}
	if first.IndexOfID(first.Messages[2].ID) != 2 || first.IndexOfID("missing") != -1 {
		t.Fatal("message lookup lost current identities")
	}
}

func TestMessageIDsStayOutOfContentDigests(t *testing.T) {
	plain := idFixtureMessages()
	tagged := withIDs(plain)
	dp, err := ContentDigestForMessages(plain)
	if err != nil {
		t.Fatal(err)
	}
	dt, err := ContentDigestForMessages(tagged)
	if err != nil {
		t.Fatal(err)
	}
	if dp != dt {
		t.Fatal("ContentDigestForMessages must ignore message ids")
	}
	if !messagesEqualForContent(tagged, plain) {
		t.Fatal("content comparison must ignore message ids")
	}
	if providerVisibleFingerprint(plain) != providerVisibleFingerprint(tagged) {
		t.Fatal("providerVisibleFingerprint must ignore message ids")
	}
	if coveredPrefixHash(plain, 3) != coveredPrefixHash(tagged, 3) {
		t.Fatal("coveredPrefixHash must ignore message ids")
	}

}

func TestSessionMutatorsMintMissingIDs(t *testing.T) {
	s := NewSession("sys")
	if s.Messages[0].ID == "" {
		t.Fatal("NewSession must mint the system message id")
	}
	s.Add(provider.Message{Role: provider.RoleUser, Content: "a"})
	s.AddBatch(provider.Message{Role: provider.RoleUser, Content: "b"}, provider.Message{Role: provider.RoleAssistant, Content: "c", ID: "KEEP"})
	if s.Messages[1].ID == "" || s.Messages[2].ID == "" {
		t.Fatal("Add/AddBatch must mint ids")
	}
	if s.Messages[3].ID != "KEEP" {
		t.Fatalf("AddBatch must keep a caller id, got %q", s.Messages[3].ID)
	}
	if s.Messages[len(s.Messages)-1].ID != "KEEP" {
		t.Fatal("Add changed supplied message identity")
	}
	kept := s.Messages[1].ID
	s.Rewrite([]provider.Message{s.Messages[0], s.Messages[1], {Role: provider.RoleAssistant, Content: "summary"}}, "test")
	if s.Messages[1].ID != kept || s.Messages[2].ID == "" {
		t.Fatal("Rewrite must keep existing ids and mint new ones")
	}
	s.Replace([]provider.Message{{Role: provider.RoleUser, Content: "x"}})
	s.ReplaceLocalMetadata([]provider.Message{s.Messages[0], {Role: provider.RoleAssistant, Content: "y"}})
	for i, m := range s.Messages {
		if m.ID == "" {
			t.Fatalf("message %d has no id after Replace/ReplaceLocalMetadata", i)
		}
	}
	empty := NewSession("")
	if empty.IndexOfID("x") != -1 {
		t.Fatal("empty session must contain no message identity")
	}
	empty.SetLeadingSystemPrompt("p")
	if empty.Messages[0].ID == "" {
		t.Fatal("SetLeadingSystemPrompt must mint the prepended system id")
	}
}
