package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"deepzora/internal/control"
	"deepzora/internal/servecontract"
)

func TestRemoteAgentPresetListRejectsChangedNavigation(t *testing.T) {
	for _, change := range []string{"generation", "revision", "selection", "replaced", "rehydrating"} {
		t.Run(change, func(t *testing.T) {
			a := newManualSessionTestApp(t)
			arrived, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(arrived)
				<-release
				writeTestJSON(w, control.AgentPresetList{Current: "old-session-preset", Locked: true})
			}))
			t.Cleanup(server.Close)
			tab := &remoteTab{id: "preset-remote", state: "ready", client: server.Client(), base: server.URL,
				capabilities: map[string]bool{serveCapabilityAgentPresetV1: true},
				routing:      remoteTabSessionRouting{currentPath: "session-id:preset-session"}}
			a.remoteTabs = map[string]*remoteTab{tab.id: tab}
			done := make(chan error, 1)
			go func() { _, err := a.ListAgentPresets(tab.id); done <- err }()
			<-arrived
			a.remoteTabMu.Lock()
			switch change {
			case "generation":
				tab.gen++
			case "revision":
				tab.routing.pathRevision++
			case "selection":
				tab.selectionRevision++
			case "replaced":
				a.remoteTabs[tab.id] = &remoteTab{id: tab.id}
			case "rehydrating":
				tab.routing.rehydratingPath = "session-id:next-session"
			}
			a.remoteTabMu.Unlock()
			close(release)
			if err := <-done; err == nil {
				t.Fatal("accepted a stale remote preset list")
			}
			a.remoteTabMu.Lock()
			cached := tab.agentPreset.current
			a.remoteTabMu.Unlock()
			if cached != "" {
				t.Fatalf("stale result changed cached preset to %q", cached)
			}
		})
	}
}

type remotePresetMarshalAction func()

func (change remotePresetMarshalAction) MarshalJSON() ([]byte, error) {
	change()
	return []byte(`{"agentPreset":"review"}`), nil
}

func remotePresetMutationCapabilities() map[string]bool {
	return map[string]bool{
		serveCapabilityAgentPresetV1: true, serveCapabilityExecutionV2: true, serveCapabilitySessions: true,
		serveCapabilitySessionIdentityV1: true, serveCapabilitySessionOwnershipV1: true,
	}
}

func TestRemoteAgentPresetMutationKeepsCapturedTargetDuringNavigation(t *testing.T) {
	for _, change := range []string{"route", "connection", "removed"} {
		t.Run(change, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.URL.Host != "original.invalid" || req.Header.Get(expectedSessionIDHeader) != "original" || req.Header.Get("Content-Type") != "application/json" {
					t.Errorf("mutation escaped its captured target: %s headers=%v", req.URL, req.Header)
				}
				return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
			})}
			tab := &remoteTab{id: "preset-remote", state: "ready", client: client, base: "http://original.invalid", gen: 3,
				capabilities: remotePresetMutationCapabilities(), routing: remoteTabSessionRouting{currentPath: "session-id:original", pathRevision: 4}, selectionRevision: 5}
			tab.capabilities[servecontract.AgentPresetCompleteV1] = true
			a := &App{remoteTabs: map[string]*remoteTab{tab.id: tab}}
			body := remotePresetMarshalAction(func() {
				a.remoteTabMu.Lock()
				defer a.remoteTabMu.Unlock()
				delete(tab.capabilities, servecontract.AgentPresetCompleteV1)
				switch change {
				case "route":
					tab.routing.currentPath = "session-id:new-session"
					tab.routing.pathRevision++
					tab.selectionRevision++
				case "connection":
					a.remoteTabs[tab.id] = &remoteTab{id: tab.id, state: "ready", client: &http.Client{}, base: "http://new.invalid", gen: 4,
						capabilities: remotePresetMutationCapabilities(), routing: remoteTabSessionRouting{currentPath: "session-id:new-session"}}
				case "removed":
					delete(a.remoteTabs, tab.id)
				}
			})
			err := a.remoteAgentPresetRequest(tab.id, http.MethodPost, "/agent-preset", body, nil, servecontract.AgentPresetCompleteV1)
			if err == nil || !strings.Contains(err.Error(), "remote session changed") || requests != 1 {
				t.Fatalf("navigation mutation result: requests=%d err=%v", requests, err)
			}
		})
	}
}

func TestRemoteAgentPresetMutationRequiresCapturedAdmission(t *testing.T) {
	for _, missing := range []string{
		serveCapabilityAgentPresetV1, serveCapabilityExecutionV2, serveCapabilitySessions,
		serveCapabilitySessionIdentityV1, serveCapabilitySessionOwnershipV1, "disconnected", "rehydrating", "missing-client", "missing-tab",
	} {
		t.Run(missing, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("inadmissible remote preset mutation reached HTTP")
				return nil, nil
			})}
			tab := &remoteTab{id: "preset-remote", state: "ready", client: client, base: "http://original.invalid",
				capabilities: remotePresetMutationCapabilities(), routing: remoteTabSessionRouting{currentPath: "session-id:original"}}
			a := &App{remoteTabs: map[string]*remoteTab{tab.id: tab}}
			switch missing {
			case "disconnected":
				tab.state = "connecting"
			case "rehydrating":
				tab.routing.rehydratingPath = "session-id:new-session"
			case "missing-client":
				tab.client = nil
			case "missing-tab":
				delete(a.remoteTabs, tab.id)
			default:
				delete(tab.capabilities, missing)
			}
			err := a.remoteAgentPresetRequest(tab.id, http.MethodPost, "/agent-preset", map[string]string{"agentPreset": "review"}, nil)
			if err == nil {
				t.Fatal("inadmissible remote preset mutation accepted")
			}
		})
	}
}
