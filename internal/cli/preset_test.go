package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/agentpreset"
	"deepzora/internal/boot"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func presetRuntimeFixture(t *testing.T) (string, *control.Controller) {
	t.Helper()
	isolateCLIConfigHome(t)
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "deepzora.toml"), []byte(minimalTestModelTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, root)
	ctrl, err := setupProfileWithOverrides(t.Context(), "", 0, false, event.Discard, cliBuildOverrides{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ctrl.Close)
	if _, err := ctrl.BindFreshSession(t.Context(), "preset-cli"); err != nil {
		t.Fatal(err)
	}
	return root, ctrl
}

func writeCLITestPreset(t *testing.T, root, id, body string, tools []string) {
	t.Helper()
	if _, err := control.SaveAgentPresetForRoot(root, "project", id, agentpreset.Render(agentpreset.Preset{
		ID: id, Name: id, Body: body, Tools: tools, KeepCoding: true,
	})); err != nil {
		t.Fatal(err)
	}
}

func TestPresetSlashSelectionRebuildsBlankSession(t *testing.T) {
	root, old := presetRuntimeFixture(t)
	writeCLITestPreset(t, root, "review", "Review every change carefully.", []string{"read_file"})
	m := newTestChatTUI()
	m.ctrl, m.modelRef = old, old.ModelRef()
	overrides := cliBuildOverrides{WorkspaceRoot: root}
	m.bindRuntimeRebuilder(0, event.Discard, false, &overrides, cliProfileBuildOptions)
	cmd := m.runSlashCommand("/preset select review")
	if cmd == nil {
		t.Fatalf("selection unavailable: %v", m.transcript)
	}
	msg := cmd().(modelSwitchMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	next := msg.ctrl.(*control.Controller)
	t.Cleanup(next.Close)
	if old.AgentPreset() != "" {
		t.Fatal("selection changed old controller before publication")
	}
	m.handleModelSwitch(msg)
	if m.ctrl != next || next.AgentPreset() != "review" {
		t.Fatalf("selected controller = %T, preset %q", m.ctrl, next.AgentPreset())
	}
	ref, _ := next.SessionRef()
	snapshot, err := next.SessionService().Query().Snapshot(t.Context(), ref)
	if err != nil || snapshot.Projection.AgentPreset != "review" {
		t.Fatalf("recorded selection = %q, %v", snapshot.Projection.AgentPreset, err)
	}
	if !strings.Contains(next.History()[0].Content, "Review every change carefully.") {
		t.Fatal("rebuilt prompt missing persona")
	}
	for _, contract := range next.ToolContractEntries() {
		if contract.Name != "read_file" && contract.Name != "use_capability" {
			t.Fatalf("preset exposes excluded tool %q", contract.Name)
		}
	}
	cmd = m.runSlashCommand("/preset select default")
	if cmd == nil {
		t.Fatal("blank custom session did not allow default selection")
	}
	defaultMsg := cmd().(modelSwitchMsg)
	if defaultMsg.err != nil {
		t.Fatal(defaultMsg.err)
	}
	t.Cleanup(defaultMsg.ctrl.Close)
	m.handleModelSwitch(defaultMsg)
	if got := defaultMsg.ctrl.(control.AgentPresets).AgentPreset(); got != "" {
		t.Fatalf("default selection = %q", got)
	}
}

func TestPresetSlashFailureKeepsController(t *testing.T) {
	_, old := presetRuntimeFixture(t)
	m := newTestChatTUI()
	m.ctrl = old
	m.rebuildRuntime = func(context.Context, controllerBuildSpec, *control.Controller) (*boot.BuildResult, error) {
		return nil, errors.New("preset disappeared")
	}
	cmd := m.runPresetCommand("/preset select missing")
	if cmd == nil {
		t.Fatal("missing selection did not run the rebuild validator")
	}
	m.handleModelSwitch(cmd().(modelSwitchMsg))
	if m.ctrl != old || old.AgentPreset() != "" || m.modelSwitchPending {
		t.Fatal("failed selection changed active controller")
	}
}

func TestPresetStartupResumeUsesTargetBeforeAssembly(t *testing.T) {
	root, original := presetRuntimeFixture(t)
	writeCLITestPreset(t, root, "review", "Old persona", []string{"read_file"})
	m := newTestChatTUI()
	m.ctrl, m.modelRef = original, original.ModelRef()
	overrides := cliBuildOverrides{WorkspaceRoot: root}
	m.bindRuntimeRebuilder(0, event.Discard, false, &overrides, cliProfileBuildOptions)
	selected := m.runPresetCommand("/preset select review")().(modelSwitchMsg)
	if selected.err != nil {
		t.Fatal(selected.err)
	}
	ctrl := selected.ctrl.(*control.Controller)
	t.Cleanup(ctrl.Close)
	m.handleModelSwitch(selected)
	ref, _ := ctrl.SessionRef()
	writeCLITestPreset(t, root, "review", "Latest persona", []string{})
	built, err := setupProfileWithOverrides(t.Context(), "", 0, false, event.Discard, cliBuildOverrides{
		WorkspaceRoot: root, AgentPreset: "default", AgentPresetSource: cliResumeTarget{ref: ref}.presetSource(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(built.Close)
	if _, bound := built.SessionRef(); bound {
		t.Fatal("source lookup bound a session before OpenSession")
	}
	if built.AgentPreset() != "review" {
		t.Fatalf("source build preset=%q", built.AgentPreset())
	}
	for _, contract := range built.ToolContractEntries() {
		if contract.Name != "use_capability" {
			t.Fatalf("empty tool preset exposes %q", contract.Name)
		}
	}
	if err := ctrl.ReleaseSessionRuntimeBinding(); err != nil {
		t.Fatal(err)
	}
	if _, err := built.OpenSession(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	prompt := built.History()[0].Content
	if !strings.Contains(prompt, "Latest persona") || strings.Contains(prompt, "Old persona") {
		t.Fatalf("resumed stale persona: %q", prompt)
	}
}

func TestPresetSlashLocksAfterTurnAndListsProblems(t *testing.T) {
	root, ctrl := presetRuntimeFixture(t)
	writeCLITestPreset(t, root, "review", "Review persona", nil)
	_, runtime, _ := ctrl.SessionBinding()
	if _, err := runtime.Session().Append(t.Context(), session.Batch{OperationID: "turn", TurnID: "turn-1", Events: []session.Event{
		{Kind: "turn/start", Payload: json.RawMessage(`{"status":"in_progress"}`)},
		{Kind: "turn/end", Payload: json.RawMessage(`{"status":"completed"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	m := newTestChatTUI()
	m.ctrl = ctrl
	if cmd := m.runPresetCommand("/preset select review"); cmd != nil {
		t.Fatal("locked session scheduled a rebuild")
	}
	m.runPresetCommand("/preset current")
	if !strings.Contains(strings.Join(m.transcript, "\n"), "default") {
		t.Fatal("current preset is not reported")
	}
	items, _, applies := m.presetArgItems("/preset select ")
	if !applies || len(items) != 0 {
		t.Fatalf("locked choices = %v, applies %v", items, applies)
	}
}

func TestPresetCanonicalResumeRefreshesSameCustomDefinition(t *testing.T) {
	root, old := presetRuntimeFixture(t)
	writeCLITestPreset(t, root, "review", "Old persona", nil)
	m := newTestChatTUI()
	m.ctrl, m.modelRef = old, old.ModelRef()
	overrides := cliBuildOverrides{WorkspaceRoot: root}
	m.bindRuntimeRebuilder(0, event.Discard, false, &overrides, cliProfileBuildOptions)
	selected := m.runPresetCommand("/preset select review")().(modelSwitchMsg)
	if selected.err != nil {
		t.Fatal(selected.err)
	}
	custom := selected.ctrl.(*control.Controller)
	t.Cleanup(custom.Close)
	m.handleModelSwitch(selected)
	custom.SetToolApprovalMode(control.ToolApprovalReadOnly)
	custom.SetPlanMode(true)
	ref, _ := custom.SessionRef()
	writeCLITestPreset(t, root, "review", "Latest persona", []string{})
	calls := 0
	m.buildController = func(spec controllerBuildSpec, _ []provider.Message, _ string, _ control.SessionAPI) (*control.Controller, error) {
		calls++
		if spec.AgentPresetSource == nil || *spec.AgentPresetSource != ref || spec.AgentPresetOverride != nil {
			t.Fatalf("resume spec = %+v", spec)
		}
		return setupProfileWithOverrides(t.Context(), spec.ModelRef, 0, false, event.Discard, cliBuildOverrides{
			WorkspaceRoot: root, AgentPresetSource: spec.AgentPresetSource,
		})
	}
	if err := m.commitCanonicalSessionSwitch(ref); err != nil {
		t.Fatal(err)
	}
	resumed := m.ctrl.(*control.Controller)
	t.Cleanup(resumed.Close)
	if resumed.ToolApprovalMode() != control.ToolApprovalReadOnly || !resumed.PlanMode() {
		t.Fatalf("preset resume changed permissions/plan: %q/%v", resumed.ToolApprovalMode(), resumed.PlanMode())
	}
	if calls != 1 || resumed == custom || resumed.AgentPreset() != "review" {
		t.Fatalf("resume builds=%d ctrl=%p old=%p preset=%q", calls, resumed, custom, resumed.AgentPreset())
	}
	if !strings.Contains(resumed.History()[0].Content, "Latest persona") || strings.Contains(resumed.History()[0].Content, "Old persona") {
		t.Fatal("explicit resume did not restore latest definition")
	}
}

func TestPresetFlagsRegisteredOnEveryEntry(t *testing.T) {
	isolateCLIConfigHome(t)
	for _, command := range []string{"chat", "run", "serve", "web"} {
		var rc int
		out := captureStdout(t, func() { rc = Run([]string{command, "--preset", "review", "--help"}, "test") })
		if rc != 0 || !strings.Contains(out, "preset") {
			t.Errorf("%s preset help exit=%d output=%q", command, rc, out)
		}
	}
}

func TestPresetCanonicalWebHandoffUsesSessionIdentity(t *testing.T) {
	_, ctrl := presetRuntimeFixture(t)
	m := newTestChatTUI()
	m.ctrl, m.modelRef = ctrl, ctrl.ModelRef()
	if cmd := m.runWebSlash(); cmd == nil {
		t.Fatal("canonical /web handoff did not schedule shutdown")
	}
	ref, _ := ctrl.SessionRef()
	if m.launchWebResumePath != cliCanonicalRoute(ref.SessionID) || m.launchWebSessionID != "" {
		t.Fatalf("handoff state = %+v", m.webHandoffState)
	}
}
