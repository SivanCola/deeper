package serve

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/provider"
)

func TestAgentPresetHTTPNewAndResumeReassemblesTargetDefinition(t *testing.T) {
	f := newPresetHTTPFixture(t, false)
	f.save(t, "project", "quiet", presetHTTPBody)
	initial := f.server.ctl()
	f.request(t, http.MethodPost, "/new", map[string]string{"agentPreset": "quiet"}, http.StatusNoContent)
	quiet := f.server.ctl().(*control.Controller)
	ref, _ := quiet.SessionRef()
	if quiet == initial || ref.SessionID == "initial" || quiet.AgentPreset() != "quiet" {
		t.Fatalf("new session = %+v preset=%q", ref, quiet.AgentPreset())
	}
	assertPresetProviderContract(t, f.run(t), "QUIET_PERSONA", true)
	f.save(t, "project", "quiet", strings.ReplaceAll(presetHTTPBody, "QUIET_PERSONA", "FRESH_PERSONA"))
	f.request(t, http.MethodPost, "/new", nil, http.StatusNoContent)
	if current := f.server.ctl().(*control.Controller); current.AgentPreset() != "quiet" || current.AgentPresetLocked() {
		t.Fatalf("new without choice lost active selection: %q locked=%v", current.AgentPreset(), current.AgentPresetLocked())
	}
	assertPresetProviderContract(t, f.run(t), "FRESH_PERSONA", true)
	f.save(t, "project", "quiet", strings.ReplaceAll(presetHTTPBody, "QUIET_PERSONA", "UPDATED_PERSONA"))
	before := f.server.ctl()
	f.request(t, http.MethodPost, "/resume", map[string]string{"hostId": ref.HostID, "sessionId": ref.SessionID}, http.StatusNoContent)
	if f.server.ctl() == before {
		t.Fatal("custom resume reused stale assembled controller")
	}
	assertPresetProviderContract(t, f.run(t), "UPDATED_PERSONA", true)
	f.request(t, http.MethodPost, "/new", map[string]string{"agentPreset": "default"}, http.StatusNoContent)
	if current := f.server.ctl().(*control.Controller); current.AgentPreset() != "" || current.AgentPresetLocked() {
		t.Fatalf("new default = %q locked=%v", current.AgentPreset(), current.AgentPresetLocked())
	}
	f.request(t, http.MethodPost, "/resume", map[string]string{"hostId": ref.HostID, "sessionId": ref.SessionID}, http.StatusNoContent)
	assertPresetProviderContract(t, f.run(t), "UPDATED_PERSONA", true)
	before = f.server.ctl()
	f.save(t, "project", "quiet", strings.ReplaceAll(presetHTTPBody, "QUIET_PERSONA", "REOPENED_PERSONA"))
	f.request(t, http.MethodPost, "/resume", map[string]string{"hostId": ref.HostID, "sessionId": ref.SessionID}, http.StatusNoContent)
	if f.server.ctl() == before {
		t.Fatal("idle custom same-identity reopen retained stale assembly")
	}
	assertPresetProviderContract(t, f.run(t), "REOPENED_PERSONA", true)
}

func TestAgentPresetHTTPResumeMissingDefinitionKeepsRecordedIdentity(t *testing.T) {
	f := newPresetHTTPFixture(t, false)
	f.save(t, "project", "quiet", presetHTTPBody)
	f.request(t, http.MethodPost, "/agent-preset", map[string]string{"agentPreset": "quiet"}, http.StatusNoContent)
	f.run(t)
	ref, _ := f.server.ctl().(*control.Controller).SessionRef()
	f.request(t, http.MethodPost, "/new", map[string]string{"agentPreset": "default"}, http.StatusNoContent)
	f.request(t, http.MethodDelete, "/agent-presets/quiet", map[string]string{"scope": "project"}, http.StatusNoContent)
	f.request(t, http.MethodPost, "/resume", map[string]string{"hostId": ref.HostID, "sessionId": ref.SessionID}, http.StatusNoContent)
	list := f.list(t)
	if list.Current != "quiet" || !list.Locked {
		t.Fatalf("missing recorded preset = %+v", list)
	}
	req := f.run(t)
	if len(req.Messages) == 0 || strings.Contains(req.Messages[0].Content, "QUIET_PERSONA") || len(req.Tools) <= 1 {
		t.Fatal("missing definition did not run default prompt/tools")
	}
}

func TestAgentPresetHTTPBusyResumeAssemblesTargetAndReattachesLiveSource(t *testing.T) {
	f := newPresetHTTPFixture(t, false)
	f.save(t, "project", "quiet", presetHTTPBody)
	f.request(t, http.MethodPost, "/new", map[string]string{"agentPreset": "quiet"}, http.StatusNoContent)
	f.run(t)
	ref, _ := f.server.ctl().(*control.Controller).SessionRef()
	f.request(t, http.MethodPost, "/new", map[string]string{"agentPreset": "default"}, http.StatusNoContent)
	busy := f.server.ctl().(*control.Controller)
	busyRef, _ := busy.SessionRef()
	f.provider.hold = make(chan struct{})
	startPresetHTTPHeldTurn(t, f)
	f.request(t, http.MethodPost, "/resume", map[string]string{"hostId": ref.HostID, "sessionId": ref.SessionID}, http.StatusNoContent)
	quiet := f.server.ctl().(*control.Controller)
	if quiet.AgentPreset() != "quiet" || !busy.Running() {
		t.Fatalf("busy switch target=%q sourceRunning=%v", quiet.AgentPreset(), busy.Running())
	}
	assertPresetProviderContract(t, startPresetHTTPHeldTurn(t, f), "QUIET_PERSONA", true)
	f.request(t, http.MethodPost, "/resume", map[string]string{"hostId": busyRef.HostID, "sessionId": busyRef.SessionID}, http.StatusNoContent)
	if f.server.ctl() != busy || !busy.Running() || !quiet.Running() {
		t.Fatal("reattach lost a live controller or turn")
	}
	busy.Cancel()
	quiet.Cancel()
	waitNotRunning(t, busy)
	waitNotRunning(t, quiet)
}

func TestAgentPresetHTTPBusyNewDetachesSourceWithChosenPreset(t *testing.T) {
	f := newPresetHTTPFixture(t, true)
	f.save(t, "project", "quiet", presetHTTPBody)
	busy := f.server.ctl().(*control.Controller)
	startPresetHTTPHeldTurn(t, f)
	f.request(t, http.MethodPost, "/agent-preset", map[string]string{"agentPreset": "quiet"}, http.StatusConflict)
	f.request(t, http.MethodPost, "/new", map[string]string{"agentPreset": "quiet"}, http.StatusNoContent)
	if f.server.ctl() == busy || !busy.Running() || f.list(t).Current != "quiet" {
		t.Fatal("busy new did not retain source and publish chosen persona")
	}
	busy.Cancel()
	waitNotRunning(t, busy)
}

func startPresetHTTPHeldTurn(t *testing.T, f *presetHTTPFixture) provider.Request {
	t.Helper()
	f.request(t, http.MethodPost, "/submit", map[string]string{"input": "hold this turn"}, http.StatusAccepted)
	select {
	case req := <-f.provider.requests:
		return req
	case <-time.After(30 * time.Second):
		t.Fatal("held fixture turn never reached provider")
		return provider.Request{}
	}
}
