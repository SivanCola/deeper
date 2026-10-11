package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/control"
)

func testTab(id, root string) *WorkspaceTab {
	return &WorkspaceTab{
		ID:            id,
		Scope:         "project",
		WorkspaceRoot: root,
		Ready:         true,
		Ctrl:          control.New(control.Options{Label: id}),
		model:         "deepseek-flash/deepseek-flash",
		mode:          "normal",
		disabledMCP:   map[string]ServerView{},
	}
}

func TestSetEffortForTabIsTabLocal(t *testing.T) {
	isolateDesktopUserDirs(t)

	rootA := t.TempDir()
	rootB := t.TempDir()
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tabA := testTab("a", rootA)
	tabB := testTab("b", rootB)
	tabA.sink = &tabEventSink{tabID: tabA.ID, app: app}
	tabB.sink = &tabEventSink{tabID: tabB.ID, app: app}
	app.tabs = map[string]*WorkspaceTab{tabA.ID: tabA, tabB.ID: tabB}
	app.tabOrder = []string{tabA.ID, tabB.ID}
	app.activeTabID = tabA.ID
	defer func() {
		for _, tab := range app.tabs {
			if tab.Ctrl != nil {
				tab.Ctrl.Close()
			}
		}
	}()

	if err := app.SetEffortForTab(tabA.ID, "max"); err != nil {
		t.Fatalf("SetEffortForTab: %v", err)
	}
	if got := app.EffortForTab(tabA.ID).Current; got != "max" {
		t.Fatalf("tab A effort = %q, want max", got)
	}
	if got := app.EffortForTab(tabB.ID).Current; got != "auto" {
		t.Fatalf("tab B effort = %q, want auto", got)
	}
	if tabB.effort != nil {
		t.Fatalf("tab B stored effort = %q, want nil", *tabB.effort)
	}
	body, err := os.ReadFile(userConfigPathForTest())
	if err == nil && strings.Contains(string(body), `effort`) {
		t.Fatalf("tab-local effort should not write provider config:\n%s", body)
	}
}

func TestEffortForTabResolvesProjectProviderConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	configBody := `default_model = "project-provider/deepseek-v4-flash"
[[providers]]
name = "project-provider"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash"]
api_key_env = "PROJECT_API_KEY"
effort = "max"
`
	if err := os.WriteFile(filepath.Join(projectRoot, "deepzora.toml"), []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, projectRoot)

	app := NewApp()
	tab := testTab("project", projectRoot)
	tab.model = "project-provider/deepseek-v4-flash"
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.activeTabID = tab.ID
	defer tab.Ctrl.Close()

	got := app.EffortForTab(tab.ID)
	if !got.Supported || got.Current != "max" {
		t.Fatalf("EffortForTab project config = %+v, want supported max", got)
	}
}

func TestEffortForTabUsesKnownModelRegistry(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	configBody := `default_model = "project-provider/deepseek-v4-flash"
[[providers]]
name = "project-provider"
kind = "openai"
base_url = "https://proxy.example.com/v1"
models = ["deepseek-v4-flash"]
api_key_env = "PROJECT_API_KEY"
`
	if err := os.WriteFile(filepath.Join(projectRoot, "deepzora.toml"), []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, projectRoot)

	app := NewApp()
	tab := testTab("project", projectRoot)
	tab.model = "project-provider/deepseek-v4-flash"
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.activeTabID = tab.ID
	defer tab.Ctrl.Close()

	got := app.EffortForTab(tab.ID)
	if !got.Supported || got.Current != "auto" || got.Default != "high" {
		t.Fatalf("EffortForTab model registry = %+v, want supported auto/high", got)
	}
	wantLevels := []string{"auto", "disabled", "low", "high", "max"}
	if len(got.Levels) != len(wantLevels) {
		t.Fatalf("levels = %v, want %v", got.Levels, wantLevels)
	}
	for i, want := range wantLevels {
		if got.Levels[i] != want {
			t.Fatalf("levels[%d] = %q, want %q", i, got.Levels[i], want)
		}
	}
}

func TestSaveTabsPersistsModelAndEffort(t *testing.T) {
	isolateDesktopUserDirs(t)

	effort := "max"
	app := NewApp()
	tab := testTab("a", t.TempDir())
	tab.effort = &effort
	tab.model = "deepseek/deepseek-v4-pro"
	tab.mode = "plan"
	tab.Ctrl.SetPlanMode(true)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()

	got := loadTabsFile()
	if len(got.Tabs) != 1 {
		t.Fatalf("tabs len = %d, want 1", len(got.Tabs))
	}
	if got.Tabs[0].Model != tab.model {
		t.Fatalf("saved model = %q, want %q", got.Tabs[0].Model, tab.model)
	}
	if got.Tabs[0].Effort == nil || *got.Tabs[0].Effort != effort {
		t.Fatalf("saved effort = %#v, want %q", got.Tabs[0].Effort, effort)
	}
	if got.Tabs[0].Mode != "plan" {
		t.Fatalf("saved mode = %q, want plan", got.Tabs[0].Mode)
	}
}

func TestSaveTabsPersistsFullAccessSeparatelyFromCollaborationMode(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	tab := testTab("a", t.TempDir())
	tab.mode = "normal"
	tab.toolApprovalMode = control.ToolApprovalDangerFullAccess
	tab.Ctrl.SetToolApprovalMode(control.ToolApprovalDangerFullAccess)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()

	got := loadTabsFile()
	if len(got.Tabs) != 1 {
		t.Fatalf("tabs len = %d, want 1", len(got.Tabs))
	}
	if got.Tabs[0].Mode != "" || got.Tabs[0].ToolApprovalMode != control.ToolApprovalDangerFullAccess {
		t.Fatalf("saved profile = mode:%q permission:%q, want normal/full access", got.Tabs[0].Mode, got.Tabs[0].ToolApprovalMode)
	}
}

func TestSaveTabsPersistsPlanAndFullAccessAsIndependentAxes(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	tab := testTab("a", t.TempDir())
	tab.mode = "plan"
	tab.toolApprovalMode = control.ToolApprovalDangerFullAccess
	tab.Ctrl.SetPlanMode(true)
	tab.Ctrl.SetToolApprovalMode(control.ToolApprovalDangerFullAccess)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()

	got := loadTabsFile()
	if len(got.Tabs) != 1 {
		t.Fatalf("tabs len = %d, want 1", len(got.Tabs))
	}
	if got.Tabs[0].Mode != "plan" || got.Tabs[0].ToolApprovalMode != control.ToolApprovalDangerFullAccess {
		t.Fatalf("saved profile = mode:%q permission:%q, want plan/full access", got.Tabs[0].Mode, got.Tabs[0].ToolApprovalMode)
	}
}

func TestSaveTabsPersistsGoalAndToolApprovalMode(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	tab := testTab("a", t.TempDir())
	tab.goal = "finish the mode redesign"
	tab.toolApprovalMode = control.ToolApprovalWorkspaceWrite
	tab.Ctrl.SetGoal(tab.goal)
	tab.Ctrl.SetToolApprovalMode(control.ToolApprovalWorkspaceWrite)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()

	got := loadTabsFile()
	if len(got.Tabs) != 1 {
		t.Fatalf("tabs len = %d, want 1", len(got.Tabs))
	}
	if got.Tabs[0].Goal != "finish the mode redesign" {
		t.Fatalf("saved goal = %q", got.Tabs[0].Goal)
	}
	if got.Tabs[0].ToolApprovalMode != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("saved tool approval mode = %q, want auto", got.Tabs[0].ToolApprovalMode)
	}
}

func TestCollaborationModesPreserveToolApprovalMode(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	tab := testTab("a", t.TempDir())
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	defer tab.Ctrl.Close()

	app.SetToolApprovalModeForTab(tab.ID, control.ToolApprovalWorkspaceWrite)
	app.SetGoalForTab(tab.ID, "finish the approval redesign")
	if got := currentTabCollaborationMode(tab); got != "goal" {
		t.Fatalf("collaboration mode = %q, want goal", got)
	}
	if tab.Ctrl.PlanMode() {
		t.Fatal("goal mode should leave plan mode off")
	}
	if got := tab.Ctrl.ToolApprovalMode(); got != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("tool approval after goal = %q, want auto", got)
	}

	app.SetCollaborationModeForTab(tab.ID, "plan")
	if got := currentTabCollaborationMode(tab); got != "plan" {
		t.Fatalf("collaboration mode = %q, want plan", got)
	}
	if got := tab.Ctrl.Goal(); got != "" {
		t.Fatalf("plan mode should clear goal, got %q", got)
	}
	if got := tab.Ctrl.ToolApprovalMode(); got != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("tool approval after plan = %q, want auto", got)
	}

	app.SetCollaborationModeForTab(tab.ID, "normal")
	if got := currentTabCollaborationMode(tab); got != "normal" {
		t.Fatalf("collaboration mode = %q, want normal", got)
	}
	if tab.Ctrl.PlanMode() {
		t.Fatal("normal mode should leave plan mode off")
	}
	if got := tab.Ctrl.ToolApprovalMode(); got != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("tool approval after normal = %q, want auto", got)
	}
}

func TestToolApprovalModesPreserveCollaborationMode(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	tab := testTab("a", t.TempDir())
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	defer tab.Ctrl.Close()

	app.SetCollaborationModeForTab(tab.ID, "plan")
	for _, mode := range []string{control.ToolApprovalReadOnly, control.ToolApprovalWorkspaceWrite, control.ToolApprovalDangerFullAccess} {
		app.SetToolApprovalModeForTab(tab.ID, mode)
		if got := currentTabCollaborationMode(tab); got != "plan" {
			t.Fatalf("collaboration after tool mode %q = %q, want plan", mode, got)
		}
		if got := tab.Ctrl.ToolApprovalMode(); got != mode {
			t.Fatalf("tool approval = %q, want %q", got, mode)
		}
	}

	app.SetGoalForTab(tab.ID, "ship the goal runner")
	app.SetToolApprovalModeForTab(tab.ID, control.ToolApprovalReadOnly)
	if got := currentTabCollaborationMode(tab); got != "goal" {
		t.Fatalf("collaboration after ask = %q, want goal", got)
	}
	app.SetToolApprovalModeForTab(tab.ID, control.ToolApprovalWorkspaceWrite)
	if got := currentTabCollaborationMode(tab); got != "goal" {
		t.Fatalf("collaboration after auto = %q, want goal", got)
	}
	app.SetToolApprovalModeForTab(tab.ID, control.ToolApprovalDangerFullAccess)
	if got := currentTabCollaborationMode(tab); got != "goal" {
		t.Fatalf("collaboration after yolo = %q, want goal", got)
	}
}

func TestSetComposerProfileForTabAppliesAllAxes(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if drained, err := app.SetComposerProfileForTab("missing", "normal", control.ToolApprovalReadOnly, ""); err == nil || drained == nil {
		t.Fatal("missing tab returned a nil drained-id slice")
	}
	tab := testTab("a", t.TempDir())
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	defer tab.Ctrl.Close()

	if _, err := app.SetComposerProfileForTab(tab.ID, "plan", control.ToolApprovalDangerFullAccess, ""); err != nil {
		t.Fatal(err)
	}
	if got := currentTabCollaborationMode(tab); got != "plan" {
		t.Fatalf("collaboration mode = %q, want plan", got)
	}
	if !tab.Ctrl.PlanMode() {
		t.Fatal("controller plan mode is off")
	}
	if got := tab.Ctrl.ToolApprovalMode(); got != control.ToolApprovalDangerFullAccess {
		t.Fatalf("tool approval mode = %q, want yolo", got)
	}

	if _, err := app.SetComposerProfileForTab(tab.ID, "goal", control.ToolApprovalWorkspaceWrite, "finish the profile migration"); err != nil {
		t.Fatal(err)
	}
	if got := currentTabCollaborationMode(tab); got != "goal" {
		t.Fatalf("collaboration mode = %q, want goal", got)
	}
	if tab.Ctrl.PlanMode() {
		t.Fatal("goal profile kept controller plan mode on")
	}
	if got := tab.Ctrl.ToolApprovalMode(); got != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("tool approval mode = %q, want auto", got)
	}
	if got := tab.Ctrl.Goal(); got != "finish the profile migration" {
		t.Fatalf("controller goal = %q", got)
	}

	got := loadTabsFile()
	if len(got.Tabs) != 1 || got.Tabs[0].Goal != "finish the profile migration" || got.Tabs[0].ToolApprovalMode != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("persisted profile = %+v", got.Tabs)
	}
}

func TestMetaReportsGoalStatus(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	tab := testTab("a", t.TempDir())
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	defer tab.Ctrl.Close()

	meta := app.MetaForTab(tab.ID)
	if meta.GoalStatus != control.GoalStatusStopped {
		t.Fatalf("initial goal status = %q, want stopped", meta.GoalStatus)
	}
	if meta.CollaborationMode != "normal" {
		t.Fatalf("initial collaboration mode = %q, want normal", meta.CollaborationMode)
	}

	app.SetGoalForTab(tab.ID, "finish the goal runner")
	meta = app.MetaForTab(tab.ID)
	if meta.Goal != "finish the goal runner" || meta.GoalStatus != control.GoalStatusRunning || meta.CollaborationMode != "normal" {
		t.Fatalf("goal meta = %+v, want running goal", meta)
	}

	app.ClearGoalForTab(tab.ID)
	meta = app.MetaForTab(tab.ID)
	if meta.Goal != "" || meta.GoalStatus != control.GoalStatusStopped || meta.CollaborationMode != "normal" {
		t.Fatalf("cleared goal meta = %+v, want stopped empty goal", meta)
	}

	app.SetCollaborationModeForTab(tab.ID, "plan")
	app.mu.Lock()
	app.mu.Unlock()
	meta = app.MetaForTab(tab.ID)
	if meta.CollaborationMode != "plan" {
		t.Fatalf("profile meta = %+v, want plan + full/standard", meta)
	}
}

func TestMetaReportsStoredCollaborationModeWhileControllerRebuilds(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	tab := testTab("a", t.TempDir())
	tab.Ctrl.Close()
	tab.Ctrl = nil
	tab.mode = "plan"
	tab.toolApprovalMode = control.ToolApprovalDangerFullAccess
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	meta := app.MetaForTab(tab.ID)
	if meta.CollaborationMode != "plan" || meta.ToolApprovalMode != control.ToolApprovalDangerFullAccess {
		t.Fatalf("rebuilding plan-yolo meta = %+v, want plan/yolo", meta)
	}

	tab.mode = "normal"
	tab.goal = "finish the goal runner"
	meta = app.MetaForTab(tab.ID)
	if meta.CollaborationMode != "normal" || meta.Goal != "finish the goal runner" || meta.GoalStatus != control.GoalStatusRunning {
		t.Fatalf("rebuilding goal meta = %+v, want running goal", meta)
	}
}

func TestSetPlanModePreservesFullAccess(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	tab := testTab("a", t.TempDir())
	tab.Ctrl.SetToolApprovalMode(control.ToolApprovalDangerFullAccess)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	app.SetPlanMode(true)
	if !tab.Ctrl.PlanMode() || !tab.Ctrl.AutoApproveTools() {
		t.Fatalf("after SetPlanMode(true): plan=%v autoApproveTools=%v, want true/true", tab.Ctrl.PlanMode(), tab.Ctrl.AutoApproveTools())
	}
	if got := currentTabCollaborationMode(tab); got != "plan" {
		t.Fatalf("collaboration mode = %q, want plan", got)
	}

	app.SetPlanMode(false)
	if tab.Ctrl.PlanMode() || !tab.Ctrl.AutoApproveTools() {
		t.Fatalf("after SetPlanMode(false): plan=%v autoApproveTools=%v, want false/true", tab.Ctrl.PlanMode(), tab.Ctrl.AutoApproveTools())
	}
}

func userConfigPathForTest() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return dir + "/deepzora/deepzora.toml"
	}
	return ""
}
