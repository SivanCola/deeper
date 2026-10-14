package main

import (
	"encoding/json"
	"strings"
	"testing"

	"deepzora/internal/plugin"
)

func TestMCPServerViewSessionDiagnosticsRemainOptional(t *testing.T) {
	legacy, err := json.Marshal(ServerView{Name: "legacy", Status: "connected"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"protocolVersion", "sessionState", "reconnectAttempts", "errorKind"} {
		if strings.Contains(string(legacy), field) {
			t.Fatalf("legacy payload unexpectedly contains optional %s: %s", field, legacy)
		}
	}

	var current ServerView
	if err := json.Unmarshal([]byte("{\n\t\t\"name\":\"current\",\"status\":\"connected\",\"protocolVersion\":\"2026-07-28\",\n\t\t\"sessionState\":\"reconnecting\",\"reconnectAttempts\":2,\"errorKind\":\"session_missing\"\n\t}"), &current); err != nil {
		t.Fatal(err)
	}
	if current.ProtocolVersion != "2026-07-28" || current.SessionState != "reconnecting" ||
		current.ReconnectAttempts != 2 || current.ErrorKind != "session_missing" {
		t.Fatalf("current session diagnostics = %+v", current)
	}

	for state, want := range map[plugin.SessionState]string{
		plugin.SessionStateConnecting: "connecting", plugin.SessionStateListening: "connecting",
		plugin.SessionStateReconnecting: "connecting", plugin.SessionStateReady: "ready",
		plugin.SessionStateFailed: "issue", plugin.SessionStateClosed: "ready",
	} {
		if got := mcpSessionRuntimeState(state); got != want {
			t.Fatalf("runtime state for %q = %q, want %q", state, got, want)
		}
	}
}

func TestMCPServerViewPublishesCurrentLifecycleContract(t *testing.T) {
	for _, test := range []struct {
		name, runtime, auth, availability string
		enabled                           bool
	}{
		{"ready", "ready", "", "connected", true},
		{"reconnecting", "connecting", "", "starting", true},
		{"authentication", "issue", "required", "auth_required", true},
		{"failure", "issue", "", "start_failed", true},
		{"on-demand", "idle", "", "available_on_demand", true},
		{"disabled", "idle", "", "disabled", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := finalizeServerView(ServerView{Name: test.name, Status: "connected", Enabled: test.enabled, RuntimeState: test.runtime, AuthStatus: test.auth})
			if view.Status != "connected" || view.RuntimeState != test.runtime || view.Availability != test.availability {
				t.Fatalf("registration and session state were conflated: %+v", view)
			}
			payload, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(payload, &fields); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"enabled", "runtimeState", "availability"} {
				if _, ok := fields[field]; !ok {
					t.Fatalf("missing current state %s: %s", field, payload)
				}
			}
			for _, field := range []string{"autoStart", "startIntent", "tier"} {
				if _, ok := fields[field]; ok {
					t.Fatalf("redundant display alias %s: %s", field, payload)
				}
			}
		})
	}
}
