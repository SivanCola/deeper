package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func createQualityTestSession(t *testing.T, root string, messages []string, state []session.Event) (*session.Service, session.SessionRef) {
	t.Helper()
	persistence := session.NewFilesystemPersistence(root)
	writer, err := persistence.Create(session.CreateOptions{SessionID: "private-session-id"})
	if err != nil {
		t.Fatal(err)
	}
	events := append([]session.Event{}, state...)
	events = append(events, session.Event{Kind: "turn/start"})
	for i, raw := range messages {
		var message provider.Message
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			t.Fatal(err)
		}
		message.ID = fmt.Sprintf("message-%d", i)
		payload, err := json.Marshal(map[string]any{"message": message})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, session.Event{Kind: "message/complete", Payload: payload})
	}
	events = append(events, session.Event{Kind: "turn/end", Payload: json.RawMessage(`{"status":"interrupted"}`)})
	if _, err := writer.Append(t.Context(), session.Batch{OperationID: "seed", TurnID: "turn", Events: events}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	service, err := session.NewService("local", persistence)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return service, session.SessionRef{HostID: "local", SessionID: "private-session-id"}
}

func TestQualityIgnoresOldTelemetryVersion(t *testing.T) {
	service, ref := createQualityTestSession(t, t.TempDir(), nil, nil)
	path := filepath.Join(t.TempDir(), "telemetry.json")
	body := []byte(`{"version":2,"usage":{"requestCount":99}}`)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	report, err := CollectQuality(QualityOptions{Query: service.Query(), Ref: ref, TelemetryPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if report.Usage.Available || report.Usage.Requests != 0 {
		t.Fatalf("old telemetry accepted: %+v", report.Usage)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
		t.Fatalf("old telemetry modified: %v", err)
	}
}

func TestQualityRejectsHistoricalGoalStateWithoutRepair(t *testing.T) {
	root := t.TempDir()
	service, ref := createQualityTestSession(t, root, nil, []session.Event{{Kind: "goal/state", Payload: json.RawMessage(`{"schemaVersion":1,"goal":"old objective"}`)}})
	_, err := CollectQuality(QualityOptions{Query: service.Query(), Ref: ref})
	if err == nil || !strings.Contains(err.Error(), "goal state") {
		t.Fatalf("old goal state accepted: %v", err)
	}
	if session.ProbeWriterHeld(filepath.Join(root, ref.SessionID)) {
		t.Fatal("quality inspection acquired a writer")
	}
}
