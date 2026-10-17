package serve

import (
	"encoding/json"
	"net/http"
	"testing"

	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
)

const presetHTTPBody = "---\nname: Quiet\ndescription: A quiet fixture persona\nkeep-coding-instructions: false\ntools: []\n---\nQUIET_PERSONA"

func TestAgentPresetHTTPAuthoringAndSelection(t *testing.T) {
	f := newPresetHTTPFixture(t, false)
	f.save(t, "user", "quiet", "---\nname: User quiet\n---\nUSER_PERSONA")
	f.save(t, "project", "quiet", presetHTTPBody)
	list := f.list(t)
	if list.Current != "default" || list.Locked || len(list.Problems) != 0 || len(list.Presets) != 2 {
		t.Fatalf("initial preset list = %+v", list)
	}
	preset := list.Presets[1]
	if preset.ID != "quiet" || preset.Body != "QUIET_PERSONA" || preset.KeepCoding || preset.Tools == nil || len(preset.Tools) != 0 || preset.Scope != agentpreset.ScopeProject || preset.Path == "" {
		t.Fatalf("authored preset fields = %+v", preset)
	}
	if list.Presets[0].Tools != nil {
		t.Fatal("unrestricted tools no longer encode null")
	}
	initial, _ := f.server.ctl().(*control.Controller).SessionRef()
	f.request(t, http.MethodPost, "/agent-preset", map[string]string{"agentPreset": "quiet"}, http.StatusNoContent)
	if got, _ := f.server.ctl().(*control.Controller).SessionRef(); got != initial {
		t.Fatalf("selection replaced identity: %+v, want %+v", got, initial)
	}
	if list := f.list(t); list.Current != "quiet" || list.Locked {
		t.Fatalf("selected list = %+v", list)
	}
	assertPresetProviderContract(t, f.run(t), "QUIET_PERSONA", true)
	if list := f.list(t); !list.Locked || list.Current != "quiet" {
		t.Fatalf("preset did not lock after turn: %+v", list)
	}
	before := f.server.ctl()
	f.request(t, http.MethodPost, "/agent-preset", map[string]string{"agentPreset": "default"}, http.StatusConflict)
	if f.server.ctl() != before {
		t.Fatal("locked selection changed controller")
	}
	var status struct {
		Preset string `json:"agentPreset"`
		Locked bool   `json:"agentPresetLocked"`
	}
	if err := json.Unmarshal(f.request(t, http.MethodGet, "/status?runtime=1", nil, http.StatusOK), &status); err != nil {
		t.Fatal(err)
	}
	if status.Preset != "quiet" || !status.Locked {
		t.Fatalf("status preset = %+v", status)
	}
	f.request(t, http.MethodDelete, "/agent-presets/quiet", map[string]string{"scope": "project"}, http.StatusNoContent)
	if got := f.list(t).Presets[1]; got.Scope != agentpreset.ScopeUser || got.Body != "USER_PERSONA" || got.Tools != nil {
		t.Fatalf("user definition not revealed after project delete: %+v", got)
	}
	f.request(t, http.MethodDelete, "/agent-presets/quiet", map[string]string{"scope": "user"}, http.StatusNoContent)
	if list := f.list(t); list.Current != "quiet" || len(list.Presets) != 1 {
		t.Fatalf("delete mutated active identity: %+v", list)
	}
}

func TestAgentPresetHTTPValidationDoesNotMutateSession(t *testing.T) {
	f := newPresetHTTPFixture(t, false)
	f.save(t, "project", "quiet", presetHTTPBody)
	before := f.server.ctl()
	ref, _ := before.(*control.Controller).SessionRef()
	for _, tc := range []struct {
		method, path string
		body         any
		status       int
	}{
		{http.MethodPost, "/agent-preset", map[string]string{}, 400},
		{http.MethodPost, "/agent-preset", map[string]string{"agentPreset": "../bad"}, 400},
		{http.MethodPost, "/agent-preset", map[string]string{"agentPreset": "missing"}, 404},
		{http.MethodPost, "/new", map[string]string{"agentPreset": "missing"}, 404},
		{http.MethodPut, "/agent-presets/bad", map[string]string{"scope": "other", "content": "body"}, 400},
		{http.MethodPut, "/agent-presets/bad", map[string]string{"scope": "project", "content": "---\ntool: []\n---\nbody"}, 400},
		{http.MethodPut, "/agent-presets/bad", map[string]string{"scope": "project"}, 400},
		{http.MethodPut, "/agent-presets/default", map[string]string{"scope": "project", "content": "body"}, 403},
		{http.MethodDelete, "/agent-presets/default", map[string]string{"scope": "project"}, 403},
		{http.MethodDelete, "/agent-presets/missing", map[string]string{"scope": "project"}, 404},
	} {
		f.request(t, tc.method, tc.path, tc.body, tc.status)
		if f.server.ctl() != before {
			t.Fatalf("%s %s replaced controller on failure", tc.method, tc.path)
		}
		if got, _ := before.(*control.Controller).SessionRef(); got != ref || before.(*control.Controller).AgentPreset() != "" {
			t.Fatalf("failed request changed session: %+v", got)
		}
	}
	f.request(t, http.MethodPost, "/agent-preset", map[string]string{"agentPreset": "quiet"}, http.StatusNoContent)
	f.request(t, http.MethodPost, "/agent-preset", map[string]string{"agentPreset": ""}, http.StatusNoContent)
	if list := f.list(t); list.Current != "default" || list.Locked {
		t.Fatalf("blank reset = %+v", list)
	}
}
