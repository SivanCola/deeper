package acp

import (
	"context"
	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/session"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
)

var testACPServices = struct {
	sync.Mutex
	services map[string]*session.Service
}{services: map[string]*session.Service{}}

func testACPService(dir string) *session.Service {
	if dir == "" {
		return nil
	}
	testACPServices.Lock()
	defer testACPServices.Unlock()
	if owner := testACPServices.services[dir]; owner != nil {
		return owner
	}
	owner, err := session.NewService("local", session.NewFilesystemPersistence(session.RootForSessionDir(dir)))
	if err != nil {
		panic(err)
	}
	testACPServices.services[dir] = owner
	return owner
}

func closeTestACPService(dir string) {
	testACPServices.Lock()
	owner := testACPServices.services[dir]
	delete(testACPServices.services, dir)
	testACPServices.Unlock()
	if owner != nil {
		_ = owner.CloseAll(context.Background())
	}
}

func testACPExecutor(sink event.Sink) *agent.Agent {
	return agent.New(nil, nil, agent.NewSession("test system"), agent.Options{}, sink)
}

func transcriptPath(dir, id string) string {
	return filepath.Join(session.RootForSessionDir(dir), id, "events.log")
}

func seedACPHistory(t *testing.T, dir, id string, history *agent.Session) error {
	t.Helper()
	owner := testACPService(dir)
	t.Cleanup(func() { closeTestACPService(dir) })
	runtime, err := owner.Create(t.Context(), session.CreateOptions{SessionID: id})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"messages": history.Snapshot()})
	if err != nil {
		return err
	}
	if len(history.Snapshot()) == 0 {
		payload = json.RawMessage(`{"messages":[]}`)
	}
	if _, err = runtime.Session().Append(t.Context(), session.Batch{OperationID: "seed", Events: []session.Event{{Kind: "history/replace", Payload: payload}}}); err != nil {
		return err
	}
	_, err = runtime.Session().Flush(t.Context())
	return err
}
