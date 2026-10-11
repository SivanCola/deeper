package main

import (
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
)

func writeTargetHistoryFixture(t *testing.T, dir, name, marker string) string {
	t.Helper()
	path := filepath.Join(dir, name+".jsonl")
	session := agent.NewSession("")
	for range 4 {
		session.Add(provider.Message{Role: provider.RoleUser, Content: marker + " user"})
		session.Add(provider.Message{Role: provider.RoleAssistant, Content: marker + " answer"})
	}
	if err := session.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateBranchMeta(path, false, func(meta *agent.BranchMeta) error {
		meta.Scope = "global"
		meta.TopicID = name
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return path
}
