package main

import (
	"deepzora/internal/session"
	"encoding/json"
	"testing"
)

func createCurrentRegistrySession(t *testing.T, app *App, root, id string, withMessage bool) (session.SessionRef, string) {
	t.Helper()
	workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "project", root)
	if err != nil {
		t.Fatal(err)
	}
	owner := app.desktopSessionService("")
	runtime, err := owner.Create(t.Context(), session.CreateOptions{SessionID: id, CWD: root, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.SetTitle(t.Context(), runtime.Ref(), defaultTopicTitle); err != nil {
		t.Fatal(err)
	}
	if withMessage {
		payload, _ := json.Marshal(map[string]any{"message": map[string]any{"id": "user-" + id, "role": "user", "content": "current message"}})
		if _, err := runtime.Session().AppendBatch(t.Context(), "used", []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspaceID, id, ""); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	return runtime.Ref(), workspaceID
}
