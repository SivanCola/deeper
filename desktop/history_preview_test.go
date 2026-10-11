package main

import (
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
)

func TestPreviewSessionMessagesLoadsWithoutResuming(t *testing.T) {
	dir := t.TempDir()
	session := agent.NewSession("")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "show history"})
	session.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer", ReasoningContent: "saved reasoning"})
	path := filepath.Join(dir, "session.jsonl")
	if err := session.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := previewSessionMessages(dir, path)
	if err != nil {
		t.Fatalf("previewSessionMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("preview history length = %d, want 2", len(got))
	}
	if got[1].Reasoning != "saved reasoning" {
		t.Fatalf("preview reasoning = %q, want saved reasoning", got[1].Reasoning)
	}
}

func TestPreviewSessionMessagesRestoresAppendEventUserTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	session := agent.NewSession("")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := session.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot first: %v", err)
	}
	session.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	session.Add(provider.Message{Role: provider.RoleUser, Content: "second"})
	if err := session.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot second: %v", err)
	}

	got, err := previewSessionMessages(dir, path)
	if err != nil {
		t.Fatalf("previewSessionMessages: %v", err)
	}
	if len(got) != 3 || got[2].Role != "user" || got[2].Content != "second" {
		t.Fatalf("preview history = %+v, want second user at index 2", got)
	}
	if got[2].CreatedAt <= 0 {
		t.Fatalf("append-event user timestamp was not restored: %+v", got[2])
	}
}
