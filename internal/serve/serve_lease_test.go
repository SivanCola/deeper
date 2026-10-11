package serve

import (
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
)

func saveServeTestSession(t *testing.T, path string) {
	t.Helper()
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
}
