package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
)

func TestRemoteAgentPresetsAreCapabilityAndSessionFenced(t *testing.T) {
	a := newManualSessionTestApp(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get(expectedSessionIDHeader) != "preset-session" {
			t.Errorf("missing expected session fence: %v", r.Header)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/agent-presets":
			writeTestJSON(w, control.AgentPresetList{Presets: []agentpreset.Preset{agentpreset.Default()}, Problems: []string{}, Current: "default"})
		case r.Method == http.MethodPut:
			var body struct{ Scope, Content string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Scope != "user" || body.Content != "review body" {
				t.Errorf("save body = %+v, %v", body, err)
			}
			writeTestJSON(w, agentpreset.Preset{ID: "review", Body: body.Content, Scope: agentpreset.ScopeUser, KeepCoding: true})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/agent-preset":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	tab := &remoteTab{id: "preset-remote", state: "ready", client: server.Client(), base: server.URL,
		capabilities: map[string]bool{}, routing: remoteTabSessionRouting{currentPath: "session-id:preset-session"}}
	a.remoteTabs = map[string]*remoteTab{tab.id: tab}
	if _, err := a.ListAgentPresets(tab.id); err == nil || requests.Load() != 0 {
		t.Fatal("unsupported remote server received preset request")
	}
	for _, cap := range []string{serveCapabilityAgentPresetV1, serveCapabilityExecutionV2, serveCapabilitySessions, serveCapabilitySessionIdentityV1, serveCapabilitySessionOwnershipV1} {
		tab.capabilities[cap] = true
	}
	list, err := a.ListAgentPresets(tab.id)
	if err != nil || list.Current != "default" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if saved, err := a.SaveAgentPreset(tab.id, "user", "review", "review body"); err != nil || saved.ID != "review" {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	if err := a.DeleteAgentPreset(tab.id, "user", "review"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetAgentPresetForTab(tab.id, "default"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 5 {
		t.Fatalf("requests = %d", requests.Load())
	}
}
