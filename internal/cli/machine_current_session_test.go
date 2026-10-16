package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/session"
)

func TestMachineSessionCatalogReadsAllCurrentPagesAndIgnoresOldTranscripts(t *testing.T) {
	dir := t.TempDir()
	service := testCLISessionService(t, dir)
	for i := range 101 {
		runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: fmt.Sprintf("current-%03d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Close(t.Context(), runtime.Ref()); err != nil {
			t.Fatal(err)
		}
	}
	writeOldTranscriptFixture(t, filepath.Join(dir, "old.jsonl"), "PRIVATE old prompt")
	entries, err := machineSessionCatalog(t.Context(), dir)
	if err != nil || len(entries) != 101 {
		t.Fatalf("current catalog = %d entries, %v", len(entries), err)
	}
	if entries[100].Ref.SessionID != "current-100" {
		t.Fatal("machine catalog lost its second page")
	}
	// Old files remain untouched and cannot leak through either machine list.
	if _, err := os.Stat(filepath.Join(dir, "old.jsonl")); err != nil {
		t.Fatal(err)
	}
	key := []byte("machine-test-identity")
	if sessions, err := machineSessions(dir, key); err != nil || len(sessions) != 0 {
		t.Fatalf("old transcript or empty current placeholder was listed: %+v, %v", sessions, err)
	}
	if tasks, err := machineTasks(dir, "", key); err != nil || len(tasks) != 0 {
		t.Fatalf("old transcript was used by task listing: %+v, %v", tasks, err)
	}
}

func TestMachineSessionStatusReportsCurrentInterruptedTurn(t *testing.T) {
	dir := t.TempDir()
	service := testCLISessionService(t, dir)
	saveMachineTestSession(t, dir, "interrupted")
	ref := session.SessionRef{HostID: "local", SessionID: "interrupted"}
	binding, err := service.Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Runtime().Session().Append(t.Context(), session.Batch{OperationID: "interrupted", TurnID: "later", Events: []session.Event{
		{Kind: "turn/start"}, {Kind: "turn/end", Payload: json.RawMessage(`{"status":"interrupted"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := binding.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	entries, err := machineSessions(dir, []byte("identity"))
	if err != nil || len(entries) != 1 || entries[0].State != "interrupted" {
		t.Fatalf("current interruption = %+v, %v", entries, err)
	}
}
