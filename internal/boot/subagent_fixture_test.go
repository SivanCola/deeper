package boot

import (
	"context"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/subagentpersist"
)

func testSubagentPersistence(t *testing.T, dir string) *subagentpersist.Store {
	t.Helper()
	parent, err := session.NewService("local", session.NewFilesystemPersistence(session.RootForSessionDir(dir)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Shutdown(context.Background()) })
	child, err := parent.Subagents()
	if err != nil {
		t.Fatal(err)
	}
	return subagentpersist.New(child)
}

func appendSubagentMessages(t *testing.T, run *agent.SubagentRun, messages ...provider.Message) {
	t.Helper()
	for i := range messages {
		if messages[i].ID == "" {
			messages[i].ID = agent.NewMessageID()
		}
	}
	if err := run.Persistence.RecordSessionMessages(t.Context(), "fixture", messages); err != nil {
		t.Fatal(err)
	}
	run.Session.AddBatch(messages...)
}
