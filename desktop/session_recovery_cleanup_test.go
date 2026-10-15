package main

import (
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
)

func saveSnapshotTurns(t *testing.T, path string, turns int) *agent.Session {
	t.Helper()
	s := agent.NewSession("sys")
	for i := range turns {
		s.Add(provider.Message{Role: provider.RoleUser, Content: "prompt " + string(rune('a'+i))})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: "reply"})
		if err := s.SaveSnapshot(path); err != nil {
			t.Fatalf("SaveSnapshot turn %d: %v", i, err)
		}
	}
	return s
}

func TestTopicTitleUserTurnsSeesEventLogTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	saveSnapshotTurns(t, path, 3)

	users := topicTitleUserTurnsFromSession(path)
	if len(users) != 3 {
		t.Fatalf("user turns = %d, want 3 (≥3-turn title upgrade depends on this)", len(users))
	}
}

func TestTopicTitleUserTurnsSkipHostFraming(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	s := agent.NewSession("sys")
	// Delivery-mode first turn: user text with the trailing runtime marker.
	// Built from the exported constant — the preview strip is byte-exact, so a
	// paraphrased marker would (correctly) not be stripped.
	s.Add(provider.Message{Role: provider.RoleUser, Content: "你是谁？\n\n" + agent.DeliveryRuntimeMarker})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "reply"})
	// Host-injected readiness nudge, persisted as role user.
	s.Add(provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "Host final-answer readiness check failed. Before giving a final answer, address the missing host-observable receipts: x"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "reply"})
	s.Add(provider.Message{Role: provider.RoleUser, Content: "帮我写一个魂斗罗游戏"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	users := topicTitleUserTurnsFromSession(path)
	if len(users) != 2 {
		t.Fatalf("user turns = %d, want 2 (readiness nudge must not count)", len(users))
	}
	if users[0] != "你是谁？" {
		t.Fatalf("first turn = %q, want the marker stripped", users[0])
	}
	if title := topicTitleFromText(users[0]); strings.Contains(title, "<delivery") || strings.Contains(title, "delivery-run") {
		t.Fatalf("title = %q, delivery marker leaked", title)
	}
}
