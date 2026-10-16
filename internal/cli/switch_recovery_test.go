package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/jobs"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func chatTUIWithRunningBackgroundJob(t *testing.T) chatTUI {
	t.Helper()
	manager := jobs.NewManager(event.Discard)
	ctrl := newCurrentTestController(t, control.Options{Jobs: manager})
	ref, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{SessionID: "switch-owner"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ctrl.Close)
	manager.StartForSession(ref.SessionID, "task", "running", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	m := newTestChatTUI()
	m.ctrl = ctrl
	m.modelRef = "deepseek-flash/deepseek-flash"
	m.buildController = func(controllerBuildSpec, control.SessionAPI) (*control.Controller, error) {
		t.Fatal("runtime switch built a replacement while a background job was running")
		return nil, nil
	}
	return m
}

func TestRuntimeSwitchesRejectRunningBackgroundJobs(t *testing.T) {
	t.Run("model", func(t *testing.T) {
		m := chatTUIWithRunningBackgroundJob(t)
		m.runModelSubcommand("/model deepseek-chat/deepseek-chat")
		if m.pendingModelSwitch != nil {
			t.Fatal("model switch queued a rebuild while a background job was running")
		}
	})

	t.Run("effort", func(t *testing.T) {
		isolateUserConfig(t)
		m := chatTUIWithRunningBackgroundJob(t)
		if cmd := m.runEffortCommand("/effort max"); cmd != nil {
			t.Fatal("effort switch queued a rebuild while a background job was running")
		}
	})

	t.Run("skill refresh", func(t *testing.T) {
		m := chatTUIWithRunningBackgroundJob(t)
		if m.scheduleSkillSessionRefresh("skill refresh", "") {
			t.Fatal("skill refresh queued a rebuild while a background job was running")
		}
	})

	t.Run("language", func(t *testing.T) {
		isolateUserConfig(t)
		m := chatTUIWithRunningBackgroundJob(t)
		if cmd := m.runLanguageSubcommand("/language zh"); cmd != nil {
			t.Fatal("language switch queued a rebuild while a background job was running")
		}
		if _, err := os.Stat(config.UserConfigPath()); !os.IsNotExist(err) {
			t.Fatalf("blocked language switch wrote config, stat err=%v", err)
		}
	})

	t.Run("currency", func(t *testing.T) {
		isolateUserConfig(t)
		m := chatTUIWithRunningBackgroundJob(t)
		if cmd := m.runCurrencySubcommand("/currency CNY"); cmd != nil {
			t.Fatal("currency switch queued a rebuild while a background job was running")
		}
		if _, err := os.Stat(config.UserConfigPath()); !os.IsNotExist(err) {
			t.Fatalf("blocked currency switch wrote config, stat err=%v", err)
		}
	})
}

type compactSnapshotSpy struct {
	control.SessionAPI
	calls int
}

func (c *compactSnapshotSpy) Snapshot() error { c.calls++; return nil }

func TestCompactOperationCompletionDoesNotRepeatMaintenanceSnapshot(t *testing.T) {
	m, ctrl, _, _ := newCanonicalTakeoverTUI(t)
	spy := &compactSnapshotSpy{SessionAPI: ctrl}
	m.ctrl = spy
	m.ingestSessionOperation(event.Event{Kind: event.SessionOperation, SessionOperation: &event.SessionOperationInfo{
		OperationID: "compact-complete", Kind: "compact", Status: "completed", Activity: "completed",
	}})
	if spy.calls != 0 {
		t.Fatalf("compact completion repeated the already committed snapshot %d times", spy.calls)
	}
}

func TestCLIControllerRebuildPreservesCurrentRuntimeAndGrants(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()
	cfg := config.Default()
	cfg.DefaultModel = "test/one"
	cfg.Providers = []config.ProviderEntry{{Name: "test", Kind: "openai", Models: []string{"one", "two"}, Model: "one", Default: "one", BaseURL: "https://example.invalid/v1"}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	service := testCLISessionService(t, filepath.Join(root, "sessions"))
	buildOptions := func(model string, _ int, _ bool, sink event.Sink, _ cliBuildOverrides) boot.Options {
		return boot.Options{Model: model, Sink: sink, ConfigSnapshot: cfg, WorkspaceRoot: root, SessionService: service}
	}
	old, err := boot.Build(t.Context(), buildOptions("test/one", 0, false, event.Discard, cliBuildOverrides{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { old.ReleaseResources(); <-old.Closed() })
	ref, err := old.BindFreshSessionWithOptions(t.Context(), session.CreateOptions{SessionID: "rebuild-current", CWD: root, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	messages := old.History()
	messages = append(messages, provider.Message{ID: "user", Role: provider.RoleUser, Content: "preserve my question", Origin: provider.MessageOriginUser}, provider.Message{ID: "answer", Role: provider.RoleAssistant, Content: "preserve my answer"})
	if err := old.AdoptRebuiltModelContext(messages); err != nil {
		t.Fatal(err)
	}
	old.RestoreSessionAuthorizations(control.SessionAuthorizations{Grants: []string{"bash(go test ./...)"}})
	old.SetToolApprovalMode(control.ToolApprovalReadOnly)
	old.SetPlanMode(true)
	messages = old.History()
	_, runtime, _ := old.SessionBinding()
	m := newTestChatTUI()
	m.ctrl, m.cfg = old, cfg
	overrides := cliBuildOverrides{}
	m.bindRuntimeRebuilder(0, event.Discard, false, &overrides, buildOptions)
	// A rejected model never relinquishes the outgoing writer or its identity.
	if _, err := m.buildController(controllerBuildSpec{ModelRef: "missing/model"}, old); err == nil {
		t.Fatal("invalid model unexpectedly produced a replacement")
	}
	if after, ok := old.SessionRef(); !ok || after != ref {
		t.Fatal("failed rebuild changed the old identity")
	}
	if err := old.Snapshot(); err != nil {
		t.Fatal(err)
	}
	next, err := m.buildController(controllerBuildSpec{ModelRef: "test/two", PlanMode: true, ToolApprovalMode: control.ToolApprovalReadOnly}, old)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { next.Close(); <-next.Closed() })
	if gotService, gotRuntime, bound := next.SessionBinding(); !bound || gotService != service || gotRuntime != runtime {
		t.Fatal("model rebuild replaced the session runtime instead of reusing it")
	}
	if got, ok := next.SessionRef(); !ok || got != ref {
		t.Fatalf("model rebuild retained a legacy execution path or changed identity: %+v", got)
	}
	if !reflect.DeepEqual(next.History(), messages) {
		t.Fatal("model rebuild changed the conversation")
	}
	if !reflect.DeepEqual(next.SessionAuthorizations(), old.SessionAuthorizations()) {
		t.Fatalf("model rebuild dropped session grants: next=%+v old=%+v", next.SessionAuthorizations(), old.SessionAuthorizations())
	}
	if next.ToolApprovalMode() != control.ToolApprovalReadOnly || !next.PlanMode() || next.SessionTemp() != old.SessionTemp() {
		t.Fatal("model rebuild lost permission, plan or temporary session state")
	}
	if err := control.ActivateSessionAPIReplacement(old, next); err != nil {
		t.Fatal(err)
	}
	old.ReleaseResources()
	if err := next.Snapshot(); err != nil {
		t.Fatalf("replacement lost the writer: %v", err)
	}
	snapshot, err := service.Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Projection.ModelMessages, messages) {
		t.Fatal("durable model context changed across the rebuild")
	}
}
