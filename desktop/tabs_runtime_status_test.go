package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/jobs"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func TestProjectTreeShowsDetachedRuntimeStatus(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	topicID := "topic_detached_status"
	topicTitle := "Detached status"
	path := "detached-current"

	app := NewApp()
	sink := &tabEventSink{tabID: "detached", app: app}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionService: app.desktopSessionService(dir), SessionCreateService: app.desktopSessionService(dir), Executor: agent.New(nil, tool.NewRegistry(), agent.NewSession(""), agent.Options{}, event.Discard), Label: "detached", Sink: sink})
	defer ctrl.Close()
	app.detachedSessions[sessionRuntimeKey(path)] = &WorkspaceTab{
		ID:            "detached",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       topicID,
		TopicTitle:    topicTitle,

		Ctrl:        ctrl,
		Ready:       true,
		sink:        sink,
		disabledMCP: map[string]ServerView{},
	}

	bindRuntimeStatusSession(t, app, ctrl, path, app.detachedSessions[sessionRuntimeKey(path)])
	ctrl.Submit("keep detached runtime running")
	<-runner.started
	nodes := currentRuntimeStatusTree(t, app)
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want one global topic", nodes)
	}
	topic := nodes[0].Children[0]
	if topic.TopicID != topicID || topic.Status != topicStatusThinking || !topic.Running {
		t.Fatalf("detached topic status = %+v, want thinking/running for %q", topic, topicID)
	}

	close(runner.release)
	waitNotRunning(t, ctrl)
}

func TestProjectTreeSplitsMultipleRuntimeSessionsInSameTopic(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	topicID := "topic_multi_runtime_status"
	topicTitle := "Multi runtime status"
	sessionA := "session-a-current"
	sessionB := "session-b-current"

	app := NewApp()
	runnerA := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	runnerB := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	asks := make(chan struct{}, 1)
	ctrlA := control.New(control.Options{Runner: runnerA, SessionDir: dir, SessionService: app.desktopSessionService(dir), SessionCreateService: app.desktopSessionService(dir), Executor: agent.New(nil, tool.NewRegistry(), agent.NewSession(""), agent.Options{}, event.Discard), Label: "a", Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.AskRequest {
			asks <- struct{}{}
		}
	})})
	ctrlB := control.New(control.Options{Runner: runnerB, SessionDir: dir, SessionService: app.desktopSessionService(dir), SessionCreateService: app.desktopSessionService(dir), Executor: agent.New(nil, tool.NewRegistry(), agent.NewSession(""), agent.Options{}, event.Discard), Label: "b", Sink: event.Discard})
	defer ctrlA.Close()
	defer ctrlB.Close()

	detached := &WorkspaceTab{
		ID:            detachedRuntimeTabID(sessionRuntimeKey(sessionA)),
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       topicID,
		TopicTitle:    topicTitle,

		Ctrl:           ctrlA,
		Ready:          true,
		ActivityStatus: topicStatusWaitingConfirmation,
		disabledMCP:    map[string]ServerView{},
	}
	visible := &WorkspaceTab{
		ID:            "visible",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       topicID,
		TopicTitle:    topicTitle,

		Ctrl:           ctrlB,
		Ready:          true,
		ActivityStatus: topicStatusThinking,
		disabledMCP:    map[string]ServerView{},
	}
	bindRuntimeStatusSession(t, app, ctrlA, sessionA, detached)
	bindRuntimeStatusSession(t, app, ctrlB, sessionB, visible)
	app.detachedSessions[sessionRuntimeKey(sessionA)] = detached
	app.tabs[visible.ID] = visible
	app.tabOrder = []string{visible.ID}
	app.activeTabID = visible.ID

	ctrlA.Submit("block A")
	ctrlB.Submit("block B")
	<-runnerA.started
	<-runnerB.started
	askCtx, cancelAsk := context.WithCancel(t.Context())
	askDone := make(chan struct{})
	go func() {
		defer close(askDone)
		_, _ = ctrlA.Ask(askCtx, []event.AskQuestion{{ID: "choice", Prompt: "Choose"}})
	}()
	defer func() { cancelAsk(); <-askDone }()
	select {
	case <-asks:
	case <-time.After(3 * time.Second):
		t.Fatal("waiting prompt was not committed")
	}

	nodes := currentRuntimeStatusTree(t, app)
	if len(nodes) != 1 || len(nodes[0].Children) != 2 {
		t.Fatalf("project tree = %#v, want two independent global sessions", nodes)
	}
	statusByPath := map[string]string{}
	for _, child := range nodes[0].Children {
		if child.TopicID != topicID || len(child.Children) != 0 {
			t.Fatalf("session projection = %#v, want flat independent row", child)
		}
		statusByPath[child.Session.SessionID] = child.Status
	}
	if statusByPath[sessionA] != topicStatusWaitingConfirmation {
		t.Fatalf("session A status = %q, want waiting; sessions=%#v", statusByPath[sessionA], nodes[0].Children)
	}
	if statusByPath[sessionB] != topicStatusThinking {
		t.Fatalf("session B status = %q, want thinking; sessions=%#v", statusByPath[sessionB], nodes[0].Children)
	}

	close(runnerA.release)
	close(runnerB.release)
	cancelAsk()
	waitNotRunning(t, ctrlA)
	waitNotRunning(t, ctrlB)
}

func TestProjectTreeShowsBackgroundJobStatus(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	topicID := "topic_background_job"
	topicTitle := "Background job"
	path := "job-current"

	jm := jobs.NewManager(event.Discard)
	release := make(chan struct{})
	jm.StartForSession(path, "bash", "sleep", func(ctx context.Context, _ io.Writer) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-release:
			return "", nil
		}
	})
	app := NewApp()
	ctrl := control.New(control.Options{SessionDir: dir, SessionService: app.desktopSessionService(dir), SessionCreateService: app.desktopSessionService(dir), Executor: agent.New(nil, tool.NewRegistry(), agent.NewSession(""), agent.Options{}, event.Discard), Label: "job", Jobs: jm})
	defer ctrl.Close()
	app.tabs["job"] = &WorkspaceTab{
		ID:            "job",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       topicID,
		TopicTitle:    topicTitle,

		Ctrl:        ctrl,
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	bindRuntimeStatusSession(t, app, ctrl, path, app.tabs["job"])
	app.tabOrder = []string{"job"}
	app.activeTabID = "job"

	nodes := currentRuntimeStatusTree(t, app)
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want one global topic", nodes)
	}
	topic := nodes[0].Children[0]
	if topic.Status != topicStatusBackgroundJob || !topic.Running {
		t.Fatalf("background job topic status = %+v, want background_job/running", topic)
	}
	tabs := app.ListTabs()
	if len(tabs) != 1 {
		t.Fatalf("tabs = %d, want 1", len(tabs))
	}
	if !tabs[0].Running || tabs[0].PendingPrompt || tabs[0].Cancellable || tabs[0].BackgroundJobs != 1 {
		t.Fatalf("tab runtime = running:%v pending:%v cancellable:%v background:%d, want background-only running tab", tabs[0].Running, tabs[0].PendingPrompt, tabs[0].Cancellable, tabs[0].BackgroundJobs)
	}
	raw, err := json.Marshal(tabs[0])
	if err != nil {
		t.Fatalf("marshal tab meta: %v", err)
	}
	if !strings.Contains(string(raw), `"cancellable":false`) {
		t.Fatalf("tab metadata should serialize explicit cancellable=false: %s", raw)
	}

	close(release)
	waitNoJobs(t, ctrl)
	nodes = mustListProjectTree(t, app)
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree after job finish = %#v, want one global topic", nodes)
	}
	topic = nodes[0].Children[0]
	if topic.Status != "" || topic.Running {
		t.Fatalf("background job topic status after finish = %+v, want idle", topic)
	}
}

func TestBackgroundJobNoticeForcesProjectTreeRefresh(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	app.ctx = context.Background()
	legacyInvalidations := 0
	app.projectTreeChangedHook = func() { legacyInvalidations++ }
	events := make(chan ProjectTreeRuntimeSnapshot, 1)
	app.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		if name == "project-tree:runtime-changed" && len(payload) == 1 {
			if snapshot, ok := payload[0].(ProjectTreeRuntimeSnapshot); ok {
				events <- snapshot
			}
		}
	}
	app.tabs["job"] = &WorkspaceTab{
		ID:          "job",
		Scope:       "global",
		TopicID:     "topic_background_notice",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	sink := &tabEventSink{tabID: "job", app: app}

	sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "background bash finished: bash-1"})
	if legacyInvalidations != 0 {
		t.Fatalf("catalog/legacy invalidations = %d, want 0 for runtime-only status", legacyInvalidations)
	}
	select {
	case snapshot := <-events:
		if snapshot.Revision == 0 || snapshot.Topics == nil {
			t.Fatalf("runtime snapshot = %+v, want versioned non-nil projection", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("background job finish notice emitted no project-tree runtime snapshot")
	}
}

func waitNoJobs(t *testing.T, ctrl control.SessionAPI) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(ctrl.Jobs()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("controller still has jobs: %+v", ctrl.Jobs())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTopicActivityStatusClearsOnCompactionDone(t *testing.T) {
	if status, ok := topicActivityStatusFromEvent(event.Event{Kind: event.CompactionStarted}); !ok || status != topicStatusThinking {
		t.Fatalf("compaction start = (%q, %v), want (%q, true)", status, ok, topicStatusThinking)
	}
	manual := event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{Trigger: agent.CompactionTriggerManual}}
	if status, ok := topicActivityStatusFromEvent(manual); !ok || status != "" {
		t.Fatalf("manual compaction done = (%q, %v), want cleared status (no thinking spinner)", status, ok)
	}
}

func TestTopicActivityStatusKeepsThinkingAfterAutomaticCompaction(t *testing.T) {
	for _, trigger := range []string{agent.CompactionTriggerPressure, agent.CompactionTriggerOverflow, agent.CompactionTriggerTool, "auto", ""} {
		done := event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{Trigger: trigger}}
		if status, ok := topicActivityStatusFromEvent(done); !ok || status != topicStatusThinking {
			t.Fatalf("compaction done (trigger %q) = (%q, %v), want (%q, true): the turn is still running", trigger, status, ok, topicStatusThinking)
		}
	}
}

func TestRuntimeDisplayStatusPreservesDeliveryCheckWhenIdle(t *testing.T) {
	got := runtimeDisplayStatus(event.RuntimeStateSnapshot{SchemaVersion: 1, Phase: "idle"}, topicStatusAwaitingDelivery)
	if got != topicStatusAwaitingDelivery {
		t.Fatalf("idle delivery check = %q, want %q", got, topicStatusAwaitingDelivery)
	}
	got = runtimeDisplayStatus(event.RuntimeStateSnapshot{SchemaVersion: 1, Phase: "executing", Running: true}, topicStatusAwaitingDelivery)
	if got != topicStatusThinking {
		t.Fatalf("running delivery check = %q, want %q", got, topicStatusThinking)
	}
	got = runtimeDisplayStatus(event.RuntimeStateSnapshot{SchemaVersion: 1, Phase: "idle"}, topicStatusPaused)
	if got != topicStatusPaused {
		t.Fatalf("idle recovery pause = %q, want %q", got, topicStatusPaused)
	}
}

func bindRuntimeStatusSession(t *testing.T, app *App, ctrl *control.Controller, path string, tab *WorkspaceTab) {
	t.Helper()
	ref, workspaceID, err := app.bindFreshDesktopSessionWithIDs(t.Context(), "global", "", ctrl, path, "")
	if err != nil {
		t.Fatal(err)
	}
	tab.SessionID, tab.SessionWorkspace.ID = ref.SessionID, workspaceID
	if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), ref.SessionID, tab.TopicID, tab.TopicTitle); err != nil {
		t.Fatal(err)
	}
	_, runtime, _ := ctrl.SessionBinding()
	appendSessionTestMessage(t, runtime, "initial-user", provider.Message{ID: "initial-user", Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "runtime status"})
	if _, _, err := app.ensureSessionOrganization("global", ""); err != nil {
		t.Fatal(err)
	}
	app.newSessionRuntimeLocked(tab, sessionRuntimeKey(tab.currentSessionIdentity()))
	t.Cleanup(func() { ctrl.Close(); app.closeSessionServices() })
}

func currentRuntimeStatusTree(t *testing.T, app *App) []ProjectNode {
	t.Helper()
	nodes := mustListProjectTree(t, app)
	live := map[string]ProjectNode{}
	for _, topic := range app.GetProjectTreeRuntimeSnapshot().Topics {
		if topic.Node.Session != nil {
			live[topic.Node.Session.SessionID] = topic.Node
		}
	}
	for i := range nodes {
		for j := range nodes[i].Children {
			row := &nodes[i].Children[j]
			if row.Session != nil {
				if runtime, ok := live[row.Session.SessionID]; ok {
					row.Status, row.Running = runtime.Status, runtime.Running
				}
			}
		}
	}
	return nodes
}
