package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

func TestDesktopAgentPresetNavigationRestoresTargetAndMissingDefinition(t *testing.T) {
	a, tab, target, root, _ := canonicalWorkspaceOpenFixture(t)
	created, err := control.SaveAgentPresetForRoot(root, "project", "review", "TARGET-PERSONA-FIRST")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"preset": "review"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Session().AppendBatch(t.Context(), "target-agent-preset", []session.Event{{Kind: session.AgentPresetKind, Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	source := session.SessionRef{HostID: localDesktopHostID, SessionID: tab.SessionID}
	openTarget := func(want string) {
		t.Helper()
		if _, err := a.OpenSession(target.Ref()); err != nil {
			t.Fatal(err)
		}
		ctrl, ok := a.controllerForTab(tab).(*control.Controller)
		if !ok || ctrl.AgentPreset() != "review" || !strings.Contains(ctrl.SystemPrompt(), want) {
			t.Fatalf("wrong target preset: %T %q", ctrl, ctrl.AgentPreset())
		}
		list, err := a.ListAgentPresets(tab.ID)
		if err != nil || list.Current != "review" {
			t.Fatalf("target list = %+v, %v", list, err)
		}
	}
	openTarget("TARGET-PERSONA-FIRST")
	if _, err := control.SaveAgentPresetForRoot(root, "project", "review", "TARGET-PERSONA-SAME-ID"); err != nil {
		t.Fatal(err)
	}
	openTarget("TARGET-PERSONA-SAME-ID")
	if err := a.controllerForTab(tab).Snapshot(); err != nil {
		t.Fatalf("refreshed same-session controller does not own execution: %v", err)
	}
	if _, err := a.OpenSession(source); err != nil {
		t.Fatal(err)
	}
	if _, err := control.SaveAgentPresetForRoot(root, "project", "review", "TARGET-PERSONA-LATEST"); err != nil {
		t.Fatal(err)
	}
	openTarget("TARGET-PERSONA-LATEST")
	if _, err := a.OpenSession(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(created.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OpenSession(target.Ref()); err != nil {
		t.Fatal(err)
	}
	ctrl := a.controllerForTab(tab).(*control.Controller)
	if ctrl.AgentPreset() != "review" || strings.Contains(ctrl.SystemPrompt(), "TARGET-PERSONA-") {
		t.Fatal("missing definition did not preserve id with default behavior")
	}
	if _, err := a.SaveAgentPreset(tab.ID, "project", agentpreset.DefaultID, "overwrite"); err == nil {
		t.Fatal("default preset was editable")
	}
}
