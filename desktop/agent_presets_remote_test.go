package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
	"deepzora/internal/servecontract"
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
			writeTestJSON(w, map[string]any{"presets": []map[string]any{{"id": "default", "keepCoding": true}}, "problems": []string{}, "current": "default"})
		case r.Method == http.MethodPut:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 2 || body["scope"] != "user" || body["content"] != "review body" {
				t.Errorf("save body = %+v, %v", body, err)
			}
			writeTestJSON(w, map[string]any{"id": "review", "body": body["content"], "scope": "user", "keepCoding": true})
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
	if err != nil || list.Current != "default" || len(list.Presets) != 1 || list.Presets[0].Complete || !list.Presets[0].IncludeRuntimeContext {
		t.Fatalf("legacy list = %+v, %v", list, err)
	}
	if saved, err := a.SaveAgentPreset(tab.id, "user", "review", "review body"); err != nil || saved.ID != "review" || saved.Complete || !saved.IncludeRuntimeContext {
		t.Fatalf("legacy save = %+v, %v", saved, err)
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

func TestRemoteAgentPresetSaveNegotiatesCompleteCapability(t *testing.T) {
	for _, tc := range []struct {
		name, metadata string
		complete       bool
		context        bool
	}{
		{name: "legacy", context: true},
		{name: "explicit-defaults", metadata: "complete: false\ninclude-runtime-context: true\n", context: true},
		{name: "complete", metadata: "complete: true\n", complete: true, context: true},
		{name: "without-context", metadata: "include-runtime-context: false\n"},
		{name: "complete-without-context", metadata: "complete: true\ninclude-runtime-context: false\n", complete: true},
	} {
		for _, supported := range []bool{false, true} {
			version := "legacy"
			if supported {
				version = "complete"
			}
			t.Run(tc.name+"/"+version, func(t *testing.T) {
				a := newManualSessionTestApp(t)
				content := "---\n" + tc.metadata + "---\nREVIEW_PERSONA"
				var requests atomic.Int32
				var stored agentpreset.Preset
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Header.Get(expectedSessionIDHeader) != "preset-session" {
						t.Errorf("missing session fence: %v", r.Header)
					}
					if r.Method == http.MethodGet && r.URL.Path == "/agent-presets" {
						writeTestJSON(w, control.AgentPresetList{Presets: []agentpreset.Preset{stored}, Current: "default"})
						return
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Method != http.MethodPut || r.URL.Path != "/agent-presets/review" || len(body) != 2 || body["scope"] != "user" || body["content"] != content {
						t.Errorf("save request = %s %s %+v, %v", r.Method, r.URL, body, err)
					}
					var err error
					stored, err = agentpreset.Parse("review", body["content"])
					if err != nil {
						t.Errorf("parse save: %v", err)
					}
					writeTestJSON(w, stored)
				}))
				t.Cleanup(server.Close)
				caps := remotePresetMutationCapabilities()
				caps[servecontract.AgentPresetCompleteV1] = supported
				tab := &remoteTab{id: "preset-remote", state: "ready", client: server.Client(), base: server.URL,
					capabilities: caps, routing: remoteTabSessionRouting{currentPath: "session-id:preset-session"}}
				a.remoteTabs = map[string]*remoteTab{tab.id: tab}
				saved, err := a.SaveAgentPreset(tab.id, "user", "review", content)
				if !supported && (tc.complete || !tc.context) {
					if err == nil || !strings.Contains(err.Error(), servecontract.AgentPresetCompleteV1) || !strings.Contains(err.Error(), "upgrade") || requests.Load() != 0 {
						t.Fatalf("unsupported save reached HTTP: requests=%d err=%v", requests.Load(), err)
					}
					return
				}
				if err != nil || saved.ID != "review" || saved.Complete != tc.complete || saved.IncludeRuntimeContext != tc.context {
					t.Fatalf("save = %+v, %v", saved, err)
				}
				list, err := a.ListAgentPresets(tab.id)
				if err != nil || len(list.Presets) != 1 || list.Presets[0].Complete != tc.complete || list.Presets[0].IncludeRuntimeContext != tc.context {
					t.Fatalf("list = %+v, %v", list, err)
				}
				if requests.Load() != 2 {
					t.Fatalf("requests = %d", requests.Load())
				}
			})
		}
	}
}
