package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/agent"
	agenttestutil "deepzora/internal/agent/testutil"
	"deepzora/internal/checkpoint"
	"deepzora/internal/command"
	"deepzora/internal/config"
	"deepzora/internal/event"
	"deepzora/internal/guardian"
	"deepzora/internal/hook"
	"deepzora/internal/i18n"
	"deepzora/internal/jobs"
	"deepzora/internal/memory"
	"deepzora/internal/permission"
	"deepzora/internal/plugin"
	"deepzora/internal/pluginpkg"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/skill"
	"deepzora/internal/testutil"
	"deepzora/internal/tool"
)

type typedNilControllerSink struct{}

func (*typedNilControllerSink) Emit(event.Event) {}

func TestResolvePlanDecisionRecordsDistinctOutcomes(t *testing.T) {
	tests := []struct {
		action PlanDecisionAction
		allow  bool
	}{
		{action: PlanDecisionStartExecution, allow: true},
		{action: PlanDecisionRevisePlan, allow: false},
		{action: PlanDecisionExitPlan, allow: false},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			session := agent.NewSession("sys")
			session.Add(provider.Message{Role: provider.RoleAssistant, Content: "proposed plan"})
			exec := agent.New(nil, nil, session, agent.Options{}, event.Discard)
			c := newOwnedTestController(t, Options{Executor: exec})
			id, reply := c.approval.registerDecisionKind(planApprovalTool, "", "", true, false, "plan")

			if err := c.ResolvePlanDecision(id, tt.action); err != nil {
				t.Fatalf("ResolvePlanDecision: %v", err)
			}
			select {
			case got := <-reply:
				if got.allow != tt.allow {
					t.Fatalf("reply allow = %v, want %v", got.allow, tt.allow)
				}
			default:
				t.Fatal("plan decision did not unblock the approval waiter")
			}

			messages := session.Snapshot()
			if len(messages) != 2 || len(messages[1].DecisionReceipts) != 1 {
				t.Fatalf("persisted messages = %+v, want receipt attached to plan answer", messages)
			}
			receipt := messages[1].DecisionReceipts[0]
			if receipt.Kind != "plan" || receipt.Outcome != string(tt.action) {
				t.Fatalf("receipt = %+v, want plan/%s", receipt, tt.action)
			}
		})
	}
}

func isolateControlConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	t.Chdir(t.TempDir())
	return home
}

type appendingRunner struct {
	session *agent.Session
}

func (r appendingRunner) Run(_ context.Context, input string) error {
	r.session.Add(provider.Message{Role: provider.RoleUser, Content: input})
	return nil
}

type handoffRunner struct {
	session *agent.Session
}

func (r handoffRunner) Run(ctx context.Context, input string) error {
	r.session.Add(provider.Message{Role: provider.RoleUser, Content: "handoff: " + input, RawContent: agent.RawUserInput(ctx, input)})
	return nil
}

type sessionContextRunner struct {
	parentSession string
	jobSession    string
}

func (r *sessionContextRunner) Run(ctx context.Context, input string) error {
	r.parentSession = agent.ParentSession(ctx)
	r.jobSession = jobs.SessionFromContext(ctx)
	return nil
}

type cancelingRunner struct {
	cancel context.CancelFunc
}

func (r cancelingRunner) Run(_ context.Context, _ string) error {
	r.cancel()
	return nil
}

// The gauge must measure what the trigger measures. Reporting the previous
// turn's billed usage is how a session displayed 8% while it was compacting.
func TestContextSnapshotMeasuresTheTriggerInput(t *testing.T) {
	prov := &scriptedTurns{turns: [][]provider.Chunk{{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 6840, CompletionTokens: 48, TotalTokens: 6888, ReasoningTokens: 48}},
		{Type: provider.ChunkDone},
	}}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{ContextWindow: 1_000_000}, event.Discard)
	c := newOwnedTestController(t, Options{Runner: ag, Executor: ag})

	if err := c.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	want := c.ContextMaintenanceSnapshot().ProjectedTokens
	used, window := c.ContextSnapshot()
	if used != want || used == 6888 || window != 1_000_000 {
		t.Fatalf("ContextSnapshot() = (%d, %d), want (%d, 1000000): the gauge reads the trigger's live input, never the last turn's 6888", used, window, want)
	}
}

type fakeControlTool struct{ name string }

func (t fakeControlTool) Name() string { return t.name }
func (fakeControlTool) Description() string {
	return "fake"
}
func (fakeControlTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (fakeControlTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (fakeControlTool) ReadOnly() bool { return true }

type startBackgroundJobTool struct {
	started chan string
	release chan struct{}
}

func TestUnboundControllerCannotAccessSessionJobs(t *testing.T) {
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	owner := newCurrentOwnedTestController(t, Options{Jobs: manager})
	unbound := newOwnedTestController(t, Options{Jobs: manager})
	ref, _ := owner.SessionRef()
	job := manager.StartForSession(ref.SessionID, "bash", "owned job", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	if unbound.CancelJob(job.ID) || len(unbound.Jobs()) != 0 || unbound.RuntimeStateSnapshot().BackgroundJobs != 0 {
		t.Fatal("unbound controller accessed another session's jobs")
	}
	if !owner.CancelJob(job.ID) {
		t.Fatal("bound controller lost cancellation authority")
	}
}

func (t startBackgroundJobTool) Name() string        { return "start_background_job" }
func (t startBackgroundJobTool) Description() string { return "start background job" }
func (t startBackgroundJobTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t startBackgroundJobTool) ReadOnly() bool { return true }
func (t startBackgroundJobTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	jm, ok := jobs.FromContext(ctx)
	if !ok {
		return "", nil
	}
	j := jm.StartForSession(jobs.SessionFromContext(ctx), "bash", "controller", func(_ context.Context, out io.Writer) (string, error) {
		_, _ = io.WriteString(out, "before\n")
		<-t.release
		_, _ = io.WriteString(out, "after\n")
		return "", nil
	})
	t.started <- j.ID
	return "started " + j.ID, nil
}

type recordingProvider struct {
	name     string
	streams  [][]provider.Chunk
	requests []provider.Request
}

func (p *recordingProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return "recording"
}

func (p *recordingProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.requests = append(p.requests, req)
	i := len(p.requests) - 1
	if i >= len(p.streams) {
		i = len(p.streams) - 1
	}
	chunks := p.streams[i]
	ch := make(chan provider.Chunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func requestMessagesText(messages []provider.Message) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func lastUserMessage(messages []provider.Message) string {
	for _, v := range slices.Backward(messages) {
		if v.Role == provider.RoleUser {
			return v.Content
		}
	}
	return ""
}

func TestNewTreatsTypedNilSinkAsDiscard(t *testing.T) {
	var sink *typedNilControllerSink
	c := newOwnedTestController(t, Options{Sink: sink})

	c.notice("typed nil sink should not panic")
}

func TestCurrentClearRetainsSourceUntilCancelledJobsExit(t *testing.T) {
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	jm := jobs.NewManager(event.Discard, jobs.WithTeardownGrace(10*time.Millisecond))
	release, started := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer func() { once.Do(func() { close(release) }); jm.Close() }()
	ctrl := newCurrentOwnedTestController(t, Options{Executor: exec, Jobs: jm})
	before, _ := ctrl.SessionRef()
	jm.StartForSession(before.SessionID, "task", "stuck clear", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		<-release
		return "", ctx.Err()
	})
	<-started
	if err := ctrl.ClearSession(); err == nil {
		t.Fatal("clear succeeded before cancelled work exited")
	}
	if after, _ := ctrl.SessionRef(); after != before {
		t.Fatal("failed clear rotated the current identity")
	}
	if _, err := ctrl.SessionService().Query().Stat(t.Context(), before); err != nil {
		t.Fatal("failed clear removed source", err)
	}
	if !jm.IsDestroying(before.SessionID) {
		t.Fatal("late writer lost its destroy gate")
	}
	once.Do(func() { close(release) })
	deadline := time.Now().Add(5 * time.Second)
	for jm.IsDestroying(before.SessionID) {
		if time.Now().After(deadline) {
			t.Fatal("destroy gate did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if err := ctrl.ClearSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.SessionService().Query().Stat(t.Context(), before); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("cleared source = %v", err)
	}
}

func TestClearSessionQueuesSessionStartHookContext(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.jsonl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte(`{"role":"user","content":"old"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	hooks := hook.NewRunner([]hook.ResolvedHook{{
		HookConfig: hook.HookConfig{Command: "session-start"},
		Event:      hook.SessionStart,
	}}, dir, func(context.Context, hook.SpawnInput) hook.SpawnResult {
		return hook.SpawnResult{ExitCode: 0, Stdout: "clear session context"}
	}, nil)
	c := newCurrentOwnedTestController(t, Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, Label: "test", Hooks: hooks})

	if err := c.ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	got := c.Compose("next")
	if !strings.Contains(got, `<hook-context event="SessionStart">`) || !strings.Contains(got, "clear session context") || !strings.HasSuffix(got, "next") {
		t.Fatalf("clear session did not queue SessionStart hook context: %q", got)
	}
}

func TestRunTurnCommitsCurrentHistoryAndActivity(t *testing.T) {
	c, service, runtime := exclusiveTestController(t, event.Discard, agenttestutil.Turn{Text: "answer"})
	if err := c.RunTurn(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	messages, err := service.Query().History(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[1].Content != "hello" || messages[2].Content != "answer" {
		t.Fatalf("durable history = %+v", messages)
	}
	info, err := service.Query().Stat(t.Context(), runtime.Ref())
	if err != nil || info.UpdatedAt.IsZero() {
		t.Fatalf("current activity = %+v, %v", info, err)
	}
}

func TestRunInjectsParentSessionForJobs(t *testing.T) {
	dir := t.TempDir()
	runner := &sessionContextRunner{}
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := newCurrentOwnedTestController(t, Options{Runner: runner, Executor: exec, SessionDir: dir, Label: "test"})
	t.Cleanup(c.Close)

	if err := c.Run(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	ref, _ := c.SessionRef()
	want := ref.SessionID
	if runner.parentSession != want {
		t.Fatalf("ParentSession = %q, want %q", runner.parentSession, want)
	}
	if runner.jobSession != want {
		t.Fatalf("jobs session = %q, want %q", runner.jobSession, want)
	}
}

func TestRunStopHookIgnoresCanceledCallerContext(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	var stopCalls int
	var stopErr error
	hooks := hook.NewRunner([]hook.ResolvedHook{{
		HookConfig: hook.HookConfig{Command: "record-stop"},
		Event:      hook.Stop,
		Scope:      hook.ScopeProject,
	}}, "", func(ctx context.Context, in hook.SpawnInput) hook.SpawnResult {
		stopCalls++
		stopErr = ctx.Err()
		return hook.SpawnResult{ExitCode: 0}
	}, nil)
	c := newOwnedTestController(t, Options{
		Runner: cancelingRunner{cancel: cancel},
		Hooks:  hooks,
	})
	t.Cleanup(c.Close)

	if err := c.Run(runCtx, "hello"); err != nil {
		t.Fatal(err)
	}

	if runCtx.Err() != context.Canceled {
		t.Fatalf("caller context err = %v, want %v", runCtx.Err(), context.Canceled)
	}
	if stopCalls != 1 {
		t.Fatalf("Stop hook calls = %d, want 1", stopCalls)
	}
	if stopErr != nil {
		t.Fatalf("Stop hook context err = %v, want nil", stopErr)
	}
}

func TestCurrentSessionPersistsBackgroundJobs(t *testing.T) {
	dir := t.TempDir()
	service, err := session.NewService("test", session.NewFilesystemPersistence(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	started := make(chan string, 1)
	release := make(chan struct{})
	jm := jobs.NewManager(event.Discard)
	reg := tool.NewRegistry()
	reg.Add(startBackgroundJobTool{started: started, release: release})
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		toolCallTurn("call-1", "start_background_job", `{}`),
		textTurn("done"),
	}}
	ag := agent.New(prov, reg, agent.NewSession("sys"), agent.Options{Jobs: jm}, event.Discard)
	c := newOwnedTestController(t, Options{Runner: ag, Executor: ag, SessionDir: dir, Label: "test", Jobs: jm,
		SessionService: service, ExclusiveSession: true})
	defer c.Close()
	ref, err := c.BindFreshSession(t.Context(), "background")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RunTurn(t.Context(), "start background job"); err != nil {
		t.Fatal(err)
	}
	jobID := <-started
	close(release)

	parentSession := ref.SessionID
	res := c.jobs.WaitForSession(context.Background(), parentSession, []string{jobID}, 1)
	if len(res) != 1 || !strings.Contains(res[0].Output, "before\n") || !strings.Contains(res[0].Output, "after\n") {
		t.Fatalf("adopted controller job = %+v, want before/after output", res)
	}
	path, err := service.SessionDir(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(jobs.ArtifactDir(path), jobID+".log")); err != nil {
		t.Fatalf("controller job artifact should be inside current session: %v", err)
	}
	c.Close()
	<-c.Closed()
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(t.TempDir(), "exported")
	if err := service.Export(t.Context(), ref, exported); err != nil {
		t.Fatal(err)
	}
	if views, err := jobs.ListArtifactViews(exported); err != nil || len(views) != 1 || views[0].ID != jobID || !views[0].ArtifactComplete {
		t.Fatalf("export lost completed job: %+v, %v", views, err)
	}
	restoredJobs := jobs.NewManager(event.Discard)
	restored := newOwnedTestController(t, Options{Executor: agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard),
		Jobs: restoredJobs, SessionService: service, ExclusiveSession: true})
	if _, err := restored.OpenSession(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	res = restoredJobs.WaitForSession(t.Context(), parentSession, []string{jobID}, 1)
	if len(res) != 1 || !strings.Contains(res[0].Output, "before\n") || !strings.Contains(res[0].Output, "after\n") {
		t.Fatalf("cold restored job = %+v", res)
	}
}

func TestRunTurnPersistsRawInputAlongsideComposedMessage(t *testing.T) {
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Runner: handoffRunner{session: sess}, Executor: exec})

	if err := c.runTurnWithRawDisplay(context.Background(), "expanded prompt", "raw prompt", "visible prompt"); err != nil {
		t.Fatal(err)
	}

	msgs := c.History()
	if len(msgs) != 2 || msgs[1].Content != "handoff: expanded prompt" || msgs[1].RawContent != "raw prompt" {
		t.Fatalf("persisted input = %+v", msgs)
	}
}

func TestSnapshotDoesNotRefreshSessionActivity(t *testing.T) {
	c, service, runtime := exclusiveTestController(t, event.Discard, agenttestutil.Turn{Text: "answer"})
	if err := c.RunTurn(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	first, err := service.Query().Stat(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Snapshot(); err != nil {
		t.Fatal(err)
	}
	second, err := service.Query().Stat(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if second.EventSequence != first.EventSequence || !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("read-only flush changed activity: before=%+v after=%+v", first, second)
	}
}

func TestCurrentMessageCommitRefreshesSessionActivity(t *testing.T) {
	c, service, runtime := exclusiveTestController(t, event.Discard, agenttestutil.Turn{Text: "first answer"}, agenttestutil.Turn{Text: "second answer"})
	if err := c.RunTurn(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	first, err := service.Query().Stat(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RunTurn(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	if err := c.Snapshot(); err != nil {
		t.Fatal(err)
	}
	second, err := service.Query().Stat(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if second.EventSequence <= first.EventSequence || !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("current turn did not advance durable activity: before=%+v after=%+v", first, second)
	}
}

func TestNewSessionStartsFreshContextAndSavesTranscript(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "old context"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := newCurrentOwnedTestController(t, Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, Label: "test"})

	before, _ := c.SessionRef()
	if err := c.NewSession(); err != nil {
		t.Fatal(err)
	}
	if after, ok := c.SessionRef(); !ok || after == before {
		t.Fatal("/new did not rotate to a fresh session path")
	}
	messages, err := c.SessionService().Query().History(t.Context(), before)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Content != "old context" {
		t.Fatalf("previous transcript was not saved: %+v", messages)
	}
	current := exec.Session().Snapshot()
	if len(current) != 1 || current[0].Role != provider.RoleSystem || current[0].Content != "sys" {
		t.Fatalf("fresh context = %+v, want only system prompt", current)
	}
}

func TestNewSessionRefusesWhileTurnRunning(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "old context"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := newCurrentOwnedTestController(t, Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, Label: "test"})

	before, _ := c.SessionRef()
	c.mu.Lock()
	c.turns.phase = session.RuntimeRunning
	c.mu.Unlock()

	if err := c.NewSession(); err == nil {
		t.Fatal("NewSession while running = nil error, want refusal")
	}
	if got, _ := c.SessionRef(); got != before {
		t.Fatalf("session identity = %+v, want unrotated %+v", got, before)
	}
	if snap := exec.Session().Snapshot(); len(snap) != 2 {
		t.Fatalf("running session was reset out from under the turn: %+v", snap)
	}

	c.mu.Lock()
	c.turns.phase = session.RuntimeIdle
	c.mu.Unlock()
	if err := c.NewSession(); err != nil {
		t.Fatalf("NewSession after the turn stopped: %v", err)
	}
	if after, ok := c.SessionRef(); !ok || after == before {
		t.Fatal("session path did not rotate once the turn stopped")
	}
}

// TestNewSessionRefusesTurnStartedDuringSnapshot forces the TOCTOU interleaving
// the running guard alone missed: a turn starts while NewSession is mid-Snapshot
// (running was false at the entry check), and must be refused so the executor
// session is not swapped out from under a live run loop.
func TestNewSessionRefusesTurnStartedDuringSnapshot(t *testing.T) {
	dir := t.TempDir()

	localSess := agent.NewSession("sys")
	localSess.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	localSess.Add(provider.Message{Role: provider.RoleAssistant, Content: "local"})
	localExec := agent.New(nil, nil, localSess, agent.Options{}, event.Discard)

	c := newCurrentOwnedTestController(t, Options{Executor: localExec, SystemPrompt: "sys", SessionDir: dir, Label: "test"})
	before, _ := c.SessionRef()
	c.snapshotMu.Lock()
	release := func() { c.snapshotMu.Unlock() }

	newSessionDone := make(chan error, 1)
	go func() { newSessionDone <- c.NewSession() }()

	// NewSession is now parked inside Snapshot, still holding the rotation gate.
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		rotating := c.rotating
		c.mu.Unlock()
		if rotating {
			break
		}
		if time.Now().After(deadline) {
			release()
			t.Fatal("rotation gate was not claimed")
		}
		time.Sleep(time.Millisecond)
	}

	// A turn tries to start in exactly the window the bare running check left
	// open. It must be refused rather than flip running=true and read the
	// session NewSession is about to replace.
	if err := c.RunTurn(context.Background(), "hello"); !errors.Is(err, ErrTurnRunning) {
		release()
		<-newSessionDone
		t.Fatalf("RunTurn during rotation = %v, want ErrTurnRunning", err)
	}
	if c.Running() {
		release()
		<-newSessionDone
		t.Fatal("RunTurn set running=true during a rotation")
	}
	// The live session must be untouched while the refused turn could have read
	// it: NewSession has not swapped yet (still parked before the swap).
	if snap := localExec.Session().Snapshot(); len(snap) != 3 {
		release()
		<-newSessionDone
		t.Fatalf("session mutated during rotation window: %+v", snap)
	}

	release()
	if err := <-newSessionDone; err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Rotation completed: a fresh session with only the system prompt, on a new
	// path, and a turn may start again.
	if after, ok := c.SessionRef(); !ok || after == before {
		t.Fatal("session path did not rotate")
	}
	if snap := localExec.Session().Snapshot(); len(snap) != 1 || snap[0].Role != provider.RoleSystem {
		t.Fatalf("post-rotation session = %+v, want only system prompt", snap)
	}
	if c.Running() {
		t.Fatal("rotation gate leaked: controller still marked running")
	}
}

// TestSessionMutationsRefuseWhileRotating proves every session-mutating entry
// point is wired to the same rotation gate: while a rotation is in progress
// (c.rotating held), each refuses instead of swapping/rewriting the live
// session, and a turn cannot start either. This is the TOCTOU class the bare
// Running() checks left open — a mutation slipping in mid-rotation.
func TestSessionMutationsRefuseWhileRotating(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "there"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, Label: "test"})

	// Simulate a rotation already in progress (as NewSession/ClearSession hold
	// it across their snapshot-then-swap window).
	if err := c.beginRotation(); err != nil {
		t.Fatalf("beginRotation: %v", err)
	}

	if err := c.NewSession(); !errors.Is(err, errRotationInProgress) {
		t.Fatalf("NewSession while rotating = %v, want errRotationInProgress", err)
	}
	if err := c.ClearSession(); !errors.Is(err, errRotationInProgress) {
		t.Fatalf("ClearSession while rotating = %v, want errRotationInProgress", err)
	}
	if _, err := c.Branch("x"); !errors.Is(err, errRotationInProgress) {
		t.Fatalf("Branch while rotating = %v, want errRotationInProgress", err)
	}
	if _, err := c.ForkNamed(1, "x"); !errors.Is(err, errRotationInProgress) {
		t.Fatalf("ForkNamed while rotating = %v, want errRotationInProgress", err)
	}
	if _, err := c.SwitchBranch("x"); !errors.Is(err, errRotationInProgress) {
		t.Fatalf("SwitchBranch while rotating = %v, want errRotationInProgress", err)
	}
	if err := c.Compact(context.Background(), ""); !errors.Is(err, errRotationInProgress) {
		t.Fatalf("Compact while rotating = %v, want errRotationInProgress", err)
	}
	if err := c.executeRewindCommand(0, RewindConversation); !errors.Is(err, errRotationInProgress) {
		t.Fatalf("Rewind while rotating = %v, want errRotationInProgress", err)
	}
	if err := c.SummarizeFrom(context.Background(), 1); !errors.Is(err, errRotationInProgress) {
		t.Fatalf("SummarizeFrom while rotating = %v, want errRotationInProgress", err)
	}
	if err := c.SummarizeUpTo(context.Background(), 1); !errors.Is(err, errRotationInProgress) {
		t.Fatalf("SummarizeUpTo while rotating = %v, want errRotationInProgress", err)
	}
	// A turn must not start while a rotation holds the gate.
	if err := c.RunTurn(context.Background(), "hello"); !errors.Is(err, ErrTurnRunning) {
		t.Fatalf("RunTurn while rotating = %v, want ErrTurnRunning", err)
	}
	// The live session was never touched by any refused mutation.
	if snap := exec.Session().Snapshot(); len(snap) != 3 {
		t.Fatalf("session mutated during rotation = %+v, want untouched", snap)
	}

	c.endRotation()

	// Conversely: while a turn runs, every mutation is refused with its own
	// message and the gate cannot be claimed.
	c.mu.Lock()
	c.turns.phase = session.RuntimeRunning
	c.mu.Unlock()
	if err := c.beginRotation(); !errors.Is(err, errTurnRunningRotation) {
		t.Fatalf("beginRotation while running = %v, want errTurnRunningRotation", err)
	}
	if err := c.Compact(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "cannot compact while a turn is running") {
		t.Fatalf("Compact while running = %v, want 'cannot compact' message", err)
	}
	if err := c.executeRewindCommand(0, RewindConversation); err == nil || !strings.Contains(err.Error(), "cannot rewind while a turn is running") {
		t.Fatalf("Rewind while running = %v, want 'cannot rewind' message", err)
	}
	if err := c.SummarizeFrom(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "cannot summarize while a turn is running") {
		t.Fatalf("SummarizeFrom while running = %v, want 'cannot summarize' message", err)
	}
}

func TestNewSessionQueuesSessionStartHookContext(t *testing.T) {
	dir := t.TempDir()
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	hooks := hook.NewRunner([]hook.ResolvedHook{{
		HookConfig: hook.HookConfig{Command: "session-start"},
		Event:      hook.SessionStart,
	}}, dir, func(context.Context, hook.SpawnInput) hook.SpawnResult {
		return hook.SpawnResult{ExitCode: 0, Stdout: "new session context"}
	}, nil)
	c := newCurrentOwnedTestController(t, Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, Label: "test", Hooks: hooks})

	if err := c.NewSession(); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	got := c.Compose("next")
	if !strings.Contains(got, `<hook-context event="SessionStart">`) || !strings.Contains(got, "new session context") || !strings.HasSuffix(got, "next") {
		t.Fatalf("new session did not queue SessionStart hook context: %q", got)
	}
}

func TestNewSessionResetsTwoModelPlannerContext(t *testing.T) {
	dir := t.TempDir()
	planner := &recordingProvider{name: "planner", streams: [][]provider.Chunk{
		planTurn("OLD PLAN: inspect alpha.go"),
		planTurn("NEW PLAN: inspect beta.go"),
	}}
	execProv := &recordingProvider{name: "executor", streams: [][]provider.Chunk{
		textTurn("old done"),
		textTurn("new done"),
	}}
	exec := agent.New(execProv, tool.NewRegistry(), agent.NewSession("exec sys"), agent.Options{}, event.Discard)
	plannerSess := agent.NewSession("planner sys")
	coord := agent.NewCoordinatorWithPlannerPolicy(planner, plannerSess, nil, agent.PlannerToolRegistry(tool.NewRegistry()), agent.Options{}, exec, 0, event.Discard, nil)
	c := newCurrentOwnedTestController(t, Options{Runner: coord, Executor: exec, SystemPrompt: "exec sys", SessionDir: dir, Label: "test"})
	t.Cleanup(c.Close)

	if err := c.Run(context.Background(), "old task alpha"); err != nil {
		t.Fatal(err)
	}
	if err := c.NewSession(); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(context.Background(), "new task beta"); err != nil {
		t.Fatal(err)
	}

	if len(planner.requests) != 2 {
		t.Fatalf("planner requests = %d, want 2", len(planner.requests))
	}
	second := requestMessagesText(planner.requests[1].Messages)
	if strings.Contains(second, "old task alpha") || strings.Contains(second, "OLD PLAN") {
		t.Fatalf("new planner request leaked previous session context:\n%s", second)
	}
	if !strings.Contains(second, "new task beta") {
		t.Fatalf("new planner request missing current task:\n%s", second)
	}
}

func TestTwoModelPlannerApprovalUsesHostGate(t *testing.T) {
	dir := t.TempDir()
	planner := &recordingProvider{name: "planner", streams: [][]provider.Chunk{
		approvalPlanTurn("Plan:\n1. Edit main.go"),
	}}
	execProv := &recordingProvider{name: "executor", streams: [][]provider.Chunk{
		textTurn("approved execution complete"),
	}}
	exec := agent.New(execProv, tool.NewRegistry(), agent.NewSession("exec sys"), agent.Options{}, event.Discard)
	coord := agent.NewCoordinatorWithPlannerPolicy(planner, agent.NewSession("planner sys"), nil, agent.PlannerToolRegistry(tool.NewRegistry()), agent.Options{}, exec, 0, event.Discard, nil)

	ids := make(chan string, 1)
	var prompts int
	c := newOwnedTestController(t, Options{
		Runner:       coord,
		Executor:     exec,
		SystemPrompt: "exec sys",
		SessionDir:   dir,

		Label: "test",
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind != event.ApprovalRequest {
				return
			}
			prompts++
			if e.Approval.Tool != planApprovalTool {
				t.Errorf("approval tool = %q, want %q", e.Approval.Tool, planApprovalTool)
			}
			if !strings.Contains(e.Approval.Reason, "Planner requested") {
				t.Errorf("approval reason = %q, want planner source", e.Approval.Reason)
			}
			ids <- e.Approval.ID
		}),
	})
	c.EnableInteractiveApproval()

	done := make(chan error, 1)
	go func() {
		done <- c.Run(context.Background(), "fix the planner approval bug")
	}()
	id := waitApprovalID(t, ids)
	if got := len(execProv.requests); got != 0 {
		t.Fatalf("executor requests before approval = %d, want 0", got)
	}
	c.Approve(id, true, false, false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("approved two-model turn did not finish")
	}
	if prompts != 1 {
		t.Fatalf("approval prompts = %d, want 1", prompts)
	}
	if got := len(execProv.requests); got == 0 {
		t.Fatal("executor did not run after approval")
	}
	reqText := requestMessagesText(execProv.requests[0].Messages)
	if !strings.Contains(reqText, "Deepzora executor handoff") || !strings.Contains(reqText, "Edit main.go") {
		t.Fatalf("approved executor request missing planner handoff:\n%s", reqText)
	}
}

func TestOpenSessionResetsTwoModelPlannerContext(t *testing.T) {
	dir := t.TempDir()
	planner := &recordingProvider{name: "planner", streams: [][]provider.Chunk{
		planTurn("OLD PLAN: inspect alpha.go"),
		planTurn("RESUMED PLAN: inspect gamma.go"),
	}}
	execProv := &recordingProvider{name: "executor", streams: [][]provider.Chunk{
		textTurn("old done"),
		textTurn("resumed done"),
	}}
	exec := agent.New(execProv, tool.NewRegistry(), agent.NewSession("exec sys"), agent.Options{}, event.Discard)
	plannerSess := agent.NewSession("planner sys")
	coord := agent.NewCoordinatorWithPlannerPolicy(planner, plannerSess, nil, agent.PlannerToolRegistry(tool.NewRegistry()), agent.Options{}, exec, 0, event.Discard, nil)
	service, err := session.NewService("test", session.NewFilesystemPersistence(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	c := newOwnedTestController(t, Options{Runner: coord, Executor: exec, SystemPrompt: "exec sys", SessionService: service, ExclusiveSession: true})
	if _, err := c.BindFreshSession(t.Context(), "original"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	if err := c.Run(context.Background(), "old task alpha"); err != nil {
		t.Fatal(err)
	}
	target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"messages":[{"id":"system","role":"system","content":"exec sys"},{"id":"saved-user","role":"user","content":"saved task gamma"}]}`)
	if _, err := target.Session().AppendBatch(t.Context(), "seed", []session.Event{{Kind: "history/replace", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenSession(t.Context(), target.Ref()); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(context.Background(), "continue gamma"); err != nil {
		t.Fatal(err)
	}

	if len(planner.requests) != 2 {
		t.Fatalf("planner requests = %d, want 2", len(planner.requests))
	}
	second := requestMessagesText(planner.requests[1].Messages)
	if strings.Contains(second, "old task alpha") || strings.Contains(second, "OLD PLAN") {
		t.Fatalf("resumed planner request leaked previous session context:\n%s", second)
	}
	if !strings.Contains(second, "continue gamma") {
		t.Fatalf("resumed planner request missing current task:\n%s", second)
	}
}

func TestResetPlannerSessionClearsPlannerHistory(t *testing.T) {
	dir := t.TempDir()
	planner := &recordingProvider{name: "planner", streams: [][]provider.Chunk{
		planTurn("FIRST PLAN: inspect alpha.go"),
		planTurn("SECOND PLAN: inspect beta.go"),
	}}
	execProv := &recordingProvider{name: "executor", streams: [][]provider.Chunk{
		textTurn("first done"),
		textTurn("second done"),
	}}
	exec := agent.New(execProv, tool.NewRegistry(), agent.NewSession("exec sys"), agent.Options{}, event.Discard)
	plannerSess := agent.NewSession("planner sys")
	coord := agent.NewCoordinatorWithPlannerPolicy(planner, plannerSess, nil, agent.PlannerToolRegistry(tool.NewRegistry()), agent.Options{}, exec, 0, event.Discard, nil)
	c := newOwnedTestController(t, Options{Runner: coord, Executor: exec, SystemPrompt: "exec sys", SessionDir: dir, Label: "test"})

	if err := c.Run(context.Background(), "first task"); err != nil {
		t.Fatal(err)
	}
	// Explicitly reset the planner session (simulates a tab switch).
	c.ResetPlannerSession()
	if err := c.Run(context.Background(), "second task"); err != nil {
		t.Fatal(err)
	}

	if len(planner.requests) != 2 {
		t.Fatalf("planner requests = %d, want 2", len(planner.requests))
	}
	second := requestMessagesText(planner.requests[1].Messages)
	if strings.Contains(second, "first task") || strings.Contains(second, "FIRST PLAN") {
		t.Fatalf("planner request after reset leaked previous session context:\n%s", second)
	}
	if !strings.Contains(second, "second task") {
		t.Fatalf("planner request after reset missing current task:\n%s", second)
	}
}

func TestTwoModelShortChoiceReplySkipsPlanner(t *testing.T) {
	dir := t.TempDir()
	planner := &recordingProvider{name: "planner", streams: [][]provider.Chunk{
		textTurn("planner should not run for a context-dependent choice reply"),
	}}
	execProv := &recordingProvider{name: "executor", streams: [][]provider.Chunk{
		textTurn("selected option 1"),
	}}
	execSess := agent.NewSession("exec sys")
	execSess.Add(provider.Message{Role: provider.RoleUser, Content: "先给我两个执行方案"})
	execSess.Add(provider.Message{Role: provider.RoleAssistant, Content: "两个执行方式可选：\n\n1. Subagent-Driven（推荐）\n2. 当前会话执行\n\n你选哪种？"})
	exec := agent.New(execProv, tool.NewRegistry(), execSess, agent.Options{}, event.Discard)
	coord := agent.NewCoordinatorWithPlannerPolicy(planner, agent.NewSession("planner sys"), nil, agent.PlannerToolRegistry(tool.NewRegistry()), agent.Options{}, exec, 0, event.Discard, NewPlannerPolicy())
	c := newOwnedTestController(t, Options{Runner: coord, Executor: exec, SystemPrompt: "exec sys", SessionDir: dir, Label: "test"})

	if err := c.Run(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}

	if len(planner.requests) != 0 {
		t.Fatalf("planner requests = %d, want 0 for a short context-dependent choice reply", len(planner.requests))
	}
	if len(execProv.requests) != 1 {
		t.Fatalf("executor requests = %d, want 1", len(execProv.requests))
	}
	reqText := requestMessagesText(execProv.requests[0].Messages)
	if !strings.Contains(reqText, "1. Subagent-Driven") {
		t.Fatalf("executor request lost the previous assistant options:\n%s", reqText)
	}
	if strings.Contains(reqText, "Deepzora executor handoff") {
		t.Fatalf("short choice reply should not be wrapped as a planner handoff:\n%s", reqText)
	}
	if got := agent.StripTransientUserBlocks(lastUserMessage(execProv.requests[0].Messages)); got != "1" {
		t.Fatalf("executor last user = %q, want raw choice reply", lastUserMessage(execProv.requests[0].Messages))
	}
}

func TestDisconnectMCPServerRemovesLazyPlaceholder(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeControlTool{name: "mcp__mock__connect"})
	c := newOwnedTestController(t, Options{Host: plugin.NewHost(), Registry: reg})

	if ok := c.DisconnectMCPServer("mock"); !ok {
		t.Fatal("DisconnectMCPServer returned false for a registered lazy placeholder")
	}
	if _, found := reg.Get("mcp__mock__connect"); found {
		t.Fatalf("lazy placeholder still registered after disconnect; names=%v", reg.Names())
	}
}

func TestRegisterMCPServerOnDemandDefersConnectionUntilFirstUse(t *testing.T) {
	t.Setenv("DEEPZORA_CACHE_HOME", t.TempDir())
	var requests atomic.Int32
	var initializes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			initializes.Add(1)
			result = map[string]any{
				"protocolVersion": "2026-07-28",
				"serverInfo":      map[string]any{"name": "on-demand", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echo a value.",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
	defer server.Close()

	host := plugin.NewHost()
	defer host.Close()
	reg := tool.NewRegistry()
	ctrl := newOwnedTestController(t, Options{Host: host, Registry: reg, PluginCtx: context.Background()})
	entry := config.PluginEntry{Name: "on-demand", Type: "http", URL: server.URL, Source: config.MCPSourceUserConfig}
	if _, err := ctrl.RegisterMCPServerOnDemand(entry); err != nil {
		t.Fatalf("RegisterMCPServerOnDemand: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("enable-time HTTP requests = %d, want zero", got)
	}
	connect, ok := reg.Get("mcp__on-demand__connect")
	if !ok {
		t.Fatalf("cache-miss connect stub missing; names=%v", reg.Names())
	}
	if _, err := connect.Execute(context.Background(), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "initializing on first use") {
		t.Fatalf("first-use connect result = %v, want initializing guidance", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !host.HasClient("on-demand") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !host.HasClient("on-demand") {
		t.Fatal("first tool use did not start the MCP connection")
	}
	if got := initializes.Load(); got != 1 {
		t.Fatalf("initialize calls = %d, want exactly one on-demand start", got)
	}
}

func TestControllerMCPHotLifecycleUpdatesCapabilityRuntime(t *testing.T) {
	t.Setenv("DEEPZORA_CACHE_HOME", t.TempDir())
	host := plugin.NewHost()
	defer host.Close()
	reg := tool.NewRegistry()
	runtime := agent.NewMCPCapabilityRuntime(context.Background(), host, nil, reg, nil)
	ctrl := newOwnedTestController(t, Options{
		Host: host, Registry: reg, PluginCtx: context.Background(), CapabilityRuntime: runtime,
	})
	frontend := runtime.NewFrontend(nil, nil)
	entry := config.PluginEntry{
		Name: "hot", Type: "http", URL: "http://127.0.0.1:1", Source: config.MCPSourceUserConfig,
	}

	if _, err := ctrl.RegisterMCPServerOnDemand(entry); err != nil {
		t.Fatalf("RegisterMCPServerOnDemand: %v", err)
	}
	listed, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil || !strings.Contains(listed, `"name": "hot"`) {
		t.Fatalf("hot add list = %q, %v", listed, err)
	}

	if !ctrl.UnregisterMCPServerTools("hot") {
		t.Fatal("UnregisterMCPServerTools returned false")
	}
	listed, err = frontend.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil || !strings.Contains(listed, `"status": "disabled"`) {
		t.Fatalf("disabled list = %q, %v", listed, err)
	}

	if _, err := ctrl.RegisterMCPServerOnDemand(entry); err != nil {
		t.Fatalf("re-enable RegisterMCPServerOnDemand: %v", err)
	}
	if !ctrl.DisconnectMCPServer("hot") {
		t.Fatal("DisconnectMCPServer returned false for runtime-only placeholder")
	}
	listed, err = frontend.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil || strings.Contains(listed, `"name": "hot"`) {
		t.Fatalf("runtime-only disconnect leaked list entry = %q, %v", listed, err)
	}
}

func TestAddMCPServerAuthorizesExplicitUserAddBeforeConnecting(t *testing.T) {
	var configured plugin.Spec
	c := newOwnedTestController(t, Options{
		WorkspaceRoot:    "/workspace",
		MCPConfigureSpec: func(spec *plugin.Spec) { configured = *spec },
	})

	if _, err := c.AddMCPServer(config.PluginEntry{Name: "user-added"}); err == nil {
		t.Fatal("AddMCPServer without a command unexpectedly succeeded")
	}
	if configured.ConfigSource != string(config.MCPSourceUserConfig) ||
		!configured.Authorized || configured.RequireLaunchApproval || configured.WorkspaceRoot != "/workspace" {
		t.Fatalf("configured spec = %+v, want user-authorized add-and-use policy", configured)
	}
}

func TestAddMCPServerWritesGlobalConfigWithoutShadowingProject(t *testing.T) {
	isolateControlConfigHome(t)
	workspace := t.TempDir()
	projectPath := filepath.Join(workspace, "deepzora.toml")
	if err := os.WriteFile(projectPath, []byte(`
[[plugins]]
name = "project-only"
command = "project-only"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := any(map[string]any{})
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2026-07-28",
				"serverInfo":      map[string]any{"name": "global-docs", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "search",
				"description": "Search documentation.",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()

	host := plugin.NewHost()
	defer host.Close()
	ctrl := newOwnedTestController(t, Options{Host: host, Registry: tool.NewRegistry(), PluginCtx: context.Background(), WorkspaceRoot: workspace})
	if n, err := ctrl.AddMCPServer(config.PluginEntry{Name: "global-docs", Type: "http", URL: server.URL}); err != nil || n != 1 {
		t.Fatalf("AddMCPServer(global-docs) = (%d, %v), want one connected tool", n, err)
	}
	globalCfg := config.LoadForEdit(config.UserConfigPath())
	globalEntry, found := testutil.FindPlugin(globalCfg.Plugins, "global-docs")
	if !found || globalEntry.URL != server.URL {
		t.Fatalf("global config entry = %+v, found=%v", globalEntry, found)
	}
	projectCfg := config.LoadForEdit(projectPath)
	if _, found := testutil.FindPlugin(projectCfg.Plugins, "global-docs"); found {
		t.Fatalf("global install leaked into project config: %+v", projectCfg.Plugins)
	}
	if _, found := testutil.FindPlugin(projectCfg.Plugins, "project-only"); !found {
		t.Fatalf("project config was not preserved: %+v", projectCfg.Plugins)
	}
}

func TestAddMCPServerRejectsProjectNameCollision(t *testing.T) {
	isolateControlConfigHome(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "deepzora.toml"), []byte(`
[[plugins]]
name = "shared"
command = "project-shared"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, workspace)
	ctrl := newOwnedTestController(t, Options{Host: plugin.NewHost(), WorkspaceRoot: workspace})
	defer ctrl.Close()
	if _, err := ctrl.AddMCPServer(config.PluginEntry{Name: "shared", Command: "global-shared"}); err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("AddMCPServer(shared) error = %v, want project collision", err)
	}
	if _, found := testutil.FindPlugin(config.LoadForEdit(config.UserConfigPath()).Plugins, "shared"); found {
		t.Fatal("rejected project collision created a global shadow")
	}
}

func TestConnectConfiguredProjectMCPIsTrustedByDefault(t *testing.T) {
	isolateControlConfigHome(t)
	workspace := t.TempDir()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := any(map[string]any{})
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2026-07-28",
				"serverInfo":      map[string]any{"name": "project-docs", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "search",
				"description": "Search project documentation.",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
	defer server.Close()
	if err := os.WriteFile(filepath.Join(workspace, "deepzora.toml"), fmt.Appendf(nil, `
[[plugins]]
name = "project-docs"
type = "http"
url = %q
`, server.URL), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, workspace)

	host := plugin.NewHost()
	defer host.Close()
	reg := tool.NewRegistry()
	var configured plugin.Spec
	ctrl := newOwnedTestController(t, Options{
		Host:          host,
		Registry:      reg,
		PluginCtx:     context.Background(),
		WorkspaceRoot: workspace,
		MCPConfigureSpec: func(spec *plugin.Spec) {
			configured = *spec
		},
	})

	n, err := ctrl.ConnectConfiguredMCPServer("project-docs")
	if err != nil {
		t.Fatalf("ConnectConfiguredMCPServer: %v", err)
	}
	if n != 1 || requests.Load() == 0 {
		t.Fatalf("trusted project MCP = %d tools, %d requests; want 1 tool and a live connection", n, requests.Load())
	}
	if _, ok := reg.Get("mcp__project-docs__search"); !ok {
		t.Fatalf("project MCP tool missing; names=%v", reg.Names())
	}
	if !configured.Authorized || configured.RequireLaunchApproval || configured.Dir != workspace {
		t.Fatalf("project MCP spec = %+v, want trusted project-scoped runtime", configured)
	}

	nextHost := plugin.NewHost()
	defer nextHost.Close()
	nextCtrl := newOwnedTestController(t, Options{
		Host:          nextHost,
		Registry:      tool.NewRegistry(),
		PluginCtx:     context.Background(),
		WorkspaceRoot: workspace,
	})
	if n, err := nextCtrl.ConnectConfiguredMCPServer("project-docs"); err != nil || n != 1 {
		t.Fatalf("subsequent project MCP connection = (%d, %v), want zero-confirmation trust", n, err)
	}
}

func TestConnectMCPServerAppliesConfiguredCallTimeouts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2026-07-28",
				"serverInfo":      map[string]any{"name": "timeout-test", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "slow",
				"description": "Wait until the caller cancels.",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		case "tools/call":
			<-r.Context().Done()
			return
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
	defer server.Close()

	tests := []struct {
		name           string
		defaultTimeout time.Duration
		entry          config.PluginEntry
	}{
		{
			name:           "global default",
			defaultTimeout: time.Second,
		},
		{
			name:           "server override",
			defaultTimeout: 10 * time.Second,
			entry:          config.PluginEntry{CallTimeoutSeconds: 1},
		},
		{
			name:           "tool override",
			defaultTimeout: 10 * time.Second,
			entry: config.PluginEntry{
				CallTimeoutSeconds: 10,
				ToolTimeoutSeconds: map[string]int{"slow": 1},
			},
		},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host := plugin.NewHost()
			defer host.Close()
			reg := tool.NewRegistry()
			ctrl := newOwnedTestController(t, Options{
				Host:                  host,
				Registry:              reg,
				MCPDefaultCallTimeout: tc.defaultTimeout,
			})
			entry := tc.entry
			entry.Name = fmt.Sprintf("timeout%d", i)
			entry.Type = "http"
			entry.URL = server.URL
			if _, err := ctrl.ConnectMCPServer(entry); err != nil {
				t.Fatalf("ConnectMCPServer: %v", err)
			}
			connected, ok := reg.Get("mcp__" + entry.Name + "__slow")
			if !ok {
				t.Fatalf("connected tool missing; names=%v", reg.Names())
			}
			started := time.Now()
			_, err := connected.Execute(context.Background(), json.RawMessage(`{}`))
			elapsed := time.Since(started)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("slow tool error = %v, want deadline exceeded", err)
			}
			if elapsed < 750*time.Millisecond || elapsed > 3*time.Second {
				t.Fatalf("slow tool elapsed = %v, want configured 1s timeout", elapsed)
			}
		})
	}
}

func TestUnregisterMCPServerToolsBlocksLateSharedHostSwap(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeControlTool{name: "mcp__mock__connect"})
	c := newOwnedTestController(t, Options{Host: plugin.NewHost(), Registry: reg})

	if ok := c.UnregisterMCPServerTools("mock"); !ok {
		t.Fatal("UnregisterMCPServerTools returned false")
	}
	reg.Add(fakeControlTool{name: "mcp__mock__echo"})
	if _, found := reg.Get("mcp__mock__echo"); found {
		t.Fatalf("late shared-host tool swap was accepted after unregister; names=%v", reg.Names())
	}
	reg.Add(fakeControlTool{name: "mcp__other__echo"})
	if _, found := reg.Get("mcp__other__echo"); !found {
		t.Fatalf("unregister blocked unrelated MCP tools; names=%v", reg.Names())
	}
}

func TestRemoveMCPServerRemovesUnconnectedLazyPlaceholder(t *testing.T) {
	isolateControlConfigHome(t)
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	t.Chdir(dir)
	if err := os.WriteFile("deepzora.toml", []byte(`
[[plugins]]
name = "mock"
command = "mock-mcp"
	`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	reg := tool.NewRegistry()
	reg.Add(fakeControlTool{name: "mcp__mock__connect"})
	host := plugin.NewHost()
	defer host.Close()
	spec := plugin.Spec{Name: "mock", Command: "mock-mcp", Authorized: true}
	runtime := agent.NewMCPCapabilityRuntime(context.Background(), host, []plugin.Spec{spec}, reg, nil)
	runtime.ConfigureServers([]config.PluginEntry{{Name: "mock", Command: "mock-mcp"}}, []plugin.Spec{spec}, map[string]bool{"mock": true})
	c := newOwnedTestController(t, Options{Host: host, Registry: reg, CapabilityRuntime: runtime, WorkspaceRoot: dir})

	disconnected, err := c.RemoveMCPServer("mock")
	if err != nil {
		t.Fatalf("RemoveMCPServer: %v", err)
	}
	if disconnected {
		t.Fatal("RemoveMCPServer reported a live disconnect for an unconnected lazy placeholder")
	}
	if _, found := reg.Get("mcp__mock__connect"); found {
		t.Fatalf("lazy placeholder still registered after remove; names=%v", reg.Names())
	}
	if names := c.ConfiguredMCPNames(); len(names) != 0 {
		t.Fatalf("ConfiguredMCPNames() = %v, want empty after remove", names)
	}
	proxy := runtime.NewFrontend(nil, nil)
	listed, listErr := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if listErr != nil || strings.Contains(listed, `"name": "mock"`) {
		t.Fatalf("removed server leaked through capability list = %q, %v", listed, listErr)
	}
}

func TestConfiguredMCPNamesUseControllerWorkspaceInsteadOfProcessCWD(t *testing.T) {
	isolateControlConfigHome(t)
	workspace := t.TempDir()
	other := t.TempDir()
	t.Chdir(other)
	if err := os.WriteFile(filepath.Join(workspace, "deepzora.toml"), []byte(`
[[plugins]]
name = "workspace-mcp"
command = "workspace-mcp"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, workspace)

	c := newOwnedTestController(t, Options{WorkspaceRoot: workspace, Host: plugin.NewHost()})
	defer c.Close()
	if got := c.ConfiguredMCPNames(); !reflect.DeepEqual(got, []string{"workspace-mcp"}) {
		t.Fatalf("ConfiguredMCPNames() = %v, want workspace-mcp from %s", got, workspace)
	}
	if got := c.DisconnectedMCPNames(); !reflect.DeepEqual(got, []string{"workspace-mcp"}) {
		t.Fatalf("DisconnectedMCPNames() = %v, want workspace-mcp from %s", got, workspace)
	}
}

func TestRemoveMCPServerKeepsRuntimeOnlyToolsWhenPersistenceRemovalFails(t *testing.T) {
	isolateControlConfigHome(t)
	reg := tool.NewRegistry()
	reg.Add(fakeControlTool{name: "mcp__runtime_only__echo"})
	c := newOwnedTestController(t, Options{Host: plugin.NewHost(), Registry: reg})

	if disconnected, err := c.RemoveMCPServer("runtime_only"); err == nil || disconnected || !strings.Contains(err.Error(), "no removable MCP server") {
		t.Fatalf("RemoveMCPServer(runtime_only) = (%v, %v)", disconnected, err)
	}
	if _, found := reg.Get("mcp__runtime_only__echo"); !found {
		t.Fatalf("runtime-only tool was removed despite failed persistence; names=%v", reg.Names())
	}
}

func TestRemoveMCPServerRejectsPluginManagedTools(t *testing.T) {
	home := isolateControlConfigHome(t)
	deepzoraHome := filepath.Join(home, ".deepzora")
	t.Setenv("DEEPZORA_HOME", deepzoraHome)
	root := filepath.Join(deepzoraHome, "plugins", "superpowers")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pluginpkg.NativeManifest), []byte(`{
  "apiVersion": "deepzora.com/plugin/v2",
  "name": "superpowers",
  "version": "1.0.0",
  "contributes": {
    "mcpServers": {
      "helper": {
        "command": "bin/helper"
      }
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pluginpkg.Upsert(deepzoraHome, pluginpkg.InstalledPlugin{
		Name:         "superpowers",
		Root:         "plugins/superpowers",
		Version:      "1.0.0",
		ManifestKind: "deepzora",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	reg.Add(fakeControlTool{name: "mcp__helper__echo"})
	c := newOwnedTestController(t, Options{Host: plugin.NewHost(), Registry: reg})
	disconnected, err := c.RemoveMCPServer("helper")
	if err == nil || disconnected || !strings.Contains(err.Error(), "managed by plugin") || !strings.Contains(err.Error(), "superpowers") {
		t.Fatalf("RemoveMCPServer(plugin-managed) = (%v, %v)", disconnected, err)
	}
	if _, found := reg.Get("mcp__helper__echo"); !found {
		t.Fatalf("plugin-managed tool was removed despite rejected removal; names=%v", reg.Names())
	}
	if disconnected := c.DisconnectMCPServer("helper"); !disconnected {
		t.Fatal("session-only disconnect should be allowed for a plugin-managed MCP")
	}
	if _, found := reg.Get("mcp__helper__echo"); found {
		t.Fatalf("plugin-managed tool survived session-only disconnect; names=%v", reg.Names())
	}
}

func TestRemoveMCPServerDeletesProjectMCPJSONSource(t *testing.T) {
	isolateControlConfigHome(t)
	if err := os.WriteFile(".mcp.json", []byte(`{
  "mcpServers": {
    "mock": { "command": "mock-mcp" },
    "keep": { "command": "keep-mcp" }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	reg.Add(fakeControlTool{name: "mcp__mock__connect"})
	c := newOwnedTestController(t, Options{Host: plugin.NewHost(), Registry: reg})
	disconnected, err := c.RemoveMCPServer("mock")
	if err != nil {
		t.Fatalf("RemoveMCPServer(.mcp.json): %v", err)
	}
	if disconnected {
		t.Fatal("RemoveMCPServer reported a live disconnect for an idle .mcp.json server")
	}
	if _, found := reg.Get("mcp__mock__connect"); found {
		t.Fatalf(".mcp.json placeholder survived removal; names=%v", reg.Names())
	}
	raw, err := os.ReadFile(".mcp.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"mock"`) || !strings.Contains(string(raw), `"keep"`) {
		t.Fatalf(".mcp.json removal did not preserve unrelated servers:\n%s", raw)
	}
}

// approvalIDs returns a Controller whose Sink forwards each ApprovalRequest's ID
// onto the channel, plus a counter of how many requests it emitted.
func approvalIDs(t *testing.T) (*Controller, chan string, *int) {
	ids := make(chan string, 8)
	prompts := 0
	c := newOwnedTestController(t, Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.ApprovalRequest {
			prompts++
			ids <- e.Approval.ID
		}
	})})
	return c, ids, &prompts
}

func permissionHookController(t *testing.T, match string) (*Controller, chan string, chan hook.Payload) {
	t.Helper()
	ids := make(chan string, 8)
	payloads := make(chan hook.Payload, 8)
	spawner := func(_ context.Context, in hook.SpawnInput) hook.SpawnResult {
		var payload hook.Payload
		if err := json.Unmarshal([]byte(in.Stdin), &payload); err != nil {
			t.Errorf("permission hook payload json: %v", err)
		}
		payloads <- payload
		return hook.SpawnResult{ExitCode: 0}
	}
	c := newOwnedTestController(t, Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				ids <- e.Approval.ID
			}
		}),
		Hooks: hook.NewRunner([]hook.ResolvedHook{{
			HookConfig: hook.HookConfig{Command: "notify", Match: match},
			Event:      hook.PermissionRequest,
			Scope:      hook.ScopeGlobal,
		}}, "/tmp", spawner, nil),
	})
	return c, ids, payloads
}

// claudePermissionHookController wires a Claude-imported PermissionRequest
// hook (PayloadFormat "claude") whose mock spawner always returns stdout, so
// tests can assert the hook's decision preempts the approval prompt instead
// of only notifying — matching Claude's own PermissionRequest contract.
func claudePermissionHookController(t *testing.T, exitCode int, stdout string) (*Controller, chan string) {
	t.Helper()
	ids := make(chan string, 8)
	spawner := func(_ context.Context, in hook.SpawnInput) hook.SpawnResult {
		return hook.SpawnResult{ExitCode: exitCode, Stdout: stdout}
	}
	c := newOwnedTestController(t, Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				ids <- e.Approval.ID
			}
		}),
		Hooks: hook.NewRunner([]hook.ResolvedHook{{
			HookConfig: hook.HookConfig{Command: "guard", Match: "Bash", PayloadFormat: "claude"},
			Event:      hook.PermissionRequest,
			Scope:      hook.ScopeGlobal,
		}}, "/tmp", spawner, nil),
	})
	return c, ids
}

func TestPermissionRequestClaudeHookAutoDenies(t *testing.T) {
	c, ids := claudePermissionHookController(t, 2, "")
	allow, _, err := gateApprover{c}.Approve(context.Background(), "bash", "rm -rf /", json.RawMessage(`{"command":"rm -rf /"}`))
	if err != nil {
		t.Fatalf("Approve error = %v", err)
	}
	if allow {
		t.Fatal("a Claude PermissionRequest hook exiting 2 should auto-deny")
	}
	select {
	case id := <-ids:
		t.Fatalf("auto-deny must preempt the approval prompt, but one was emitted: %s", id)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPermissionRequestClaudeHookAutoAllows(t *testing.T) {
	allowJSON := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	c, ids := claudePermissionHookController(t, 0, allowJSON)
	allow, _, err := gateApprover{c}.Approve(context.Background(), "bash", "go test ./...", json.RawMessage(`{"command":"go test ./..."}`))
	if err != nil {
		t.Fatalf("Approve error = %v", err)
	}
	if !allow {
		t.Fatal("a Claude PermissionRequest hook returning decision.behavior=allow should auto-allow")
	}
	select {
	case id := <-ids:
		t.Fatalf("auto-allow must preempt the approval prompt, but one was emitted: %s", id)
	case <-time.After(50 * time.Millisecond):
	}
}

// wildcardClaudePermissionHookController is like claudePermissionHookController
// but matches every tool, for exercising fresh-human-required tools whose
// names ("remember", "sandbox_escape", ...) aren't Claude tool names.
func wildcardClaudePermissionHookController(t *testing.T, exitCode int, stdout string) (*Controller, chan string) {
	t.Helper()
	ids := make(chan string, 8)
	spawner := func(_ context.Context, in hook.SpawnInput) hook.SpawnResult {
		return hook.SpawnResult{ExitCode: exitCode, Stdout: stdout}
	}
	c := newOwnedTestController(t, Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				ids <- e.Approval.ID
			}
		}),
		Hooks: hook.NewRunner([]hook.ResolvedHook{{
			HookConfig: hook.HookConfig{Command: "guard", PayloadFormat: "claude"},
			Event:      hook.PermissionRequest,
			Scope:      hook.ScopeGlobal,
		}}, "/tmp", spawner, nil),
	})
	return c, ids
}

func TestPermissionRequestClaudeHookCannotAutoAllowFreshHumanApproval(t *testing.T) {
	allowJSON := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	for _, tool := range []string{memoryRememberTool, memoryForgetTool, SandboxEscapeApprovalTool, ManagedConfigWriteApprovalTool} {
		t.Run(tool, func(t *testing.T) {
			c, ids := wildcardClaudePermissionHookController(t, 0, allowJSON)
			done := make(chan bool, 1)
			go func() {
				allow, _, err := gateApprover{c}.Approve(context.Background(), tool, "", json.RawMessage(`{}`))
				if err != nil {
					t.Errorf("Approve error = %v", err)
					return
				}
				done <- allow
			}()

			id := waitApprovalID(t, ids)
			c.Approve(id, true, false, false)
			select {
			case allow := <-done:
				if !allow {
					t.Fatal("manual approval should still allow")
				}
			case <-time.After(30 * time.Second):
				t.Fatal("approval stayed blocked")
			}
		})
	}
}

func TestPermissionRequestClaudeHookAutoDeniesFreshHumanApproval(t *testing.T) {
	// A deny is always safe to auto-honor, even for fresh-human tools —
	// refusing something that requires a human's blessing can't leak
	// unauthorized access the way an auto-allow could.
	for _, tool := range []string{memoryRememberTool, SandboxEscapeApprovalTool} {
		t.Run(tool, func(t *testing.T) {
			c, ids := wildcardClaudePermissionHookController(t, 2, "")
			allow, _, err := gateApprover{c}.Approve(context.Background(), tool, "", json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("Approve error = %v", err)
			}
			if allow {
				t.Fatal("a Claude PermissionRequest hook exiting 2 should auto-deny even a fresh-human tool")
			}
			select {
			case id := <-ids:
				t.Fatalf("auto-deny must preempt the approval prompt, but one was emitted: %s", id)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

// TestPermissionRequestClaudeHookCannotAutoAllowOptsFreshOnlyDecision covers
// the fresh-human protection's other branch: a tool that requestFreshApprovalDecision
// marks fresh (opts.fresh=true) without being one of
// RequiresFreshHumanApprovalTool's fixed cases. PlanModeReadOnlyCommandApprovalTool
// is exactly that — the earlier tests only exercised tools protected via
// requiresFreshApprovalTool(tool), not the opts.fresh flag alone.
func TestPermissionRequestClaudeHookCannotAutoAllowOptsFreshOnlyDecision(t *testing.T) {
	if RequiresFreshHumanApprovalTool("fresh_test_action") {
		t.Fatal("test assumes this tool is fresh-only via opts.fresh, not RequiresFreshHumanApprovalTool")
	}
	allowJSON := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	c, ids := wildcardClaudePermissionHookController(t, 0, allowJSON)
	done := make(chan bool, 1)
	go func() {
		reply, err := c.requestFreshApprovalDecision(context.Background(), "fresh_test_action", "ls", nil, "trust this read-only command prefix?")
		if err != nil {
			t.Errorf("requestFreshApprovalDecision error = %v", err)
			return
		}
		done <- reply.allow
	}()

	id := waitApprovalID(t, ids)
	c.Approve(id, true, false, false)
	select {
	case allow := <-done:
		if !allow {
			t.Fatal("manual approval should still allow")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("approval stayed blocked — a Claude hook allow should not have preempted this opts.fresh decision")
	}
}

func waitApprovalID(t *testing.T, ids <-chan string) string {
	t.Helper()
	select {
	case id := <-ids:
		return id
	case <-time.After(30 * time.Second):
		t.Fatal("ApprovalRequest was not emitted")
	}
	return ""
}

func waitPermissionHook(t *testing.T, payloads <-chan hook.Payload) hook.Payload {
	t.Helper()
	select {
	case payload := <-payloads:
		return payload
	case <-time.After(30 * time.Second):
		t.Fatal("PermissionRequest hook did not fire")
	}
	return hook.Payload{}
}

func assertNoPermissionHook(t *testing.T, payloads <-chan hook.Payload) {
	t.Helper()
	select {
	case payload := <-payloads:
		t.Fatalf("PermissionRequest hook fired unexpectedly: %+v", payload)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestApprovalAllowOnce drives the happy path: the gate emits an ApprovalRequest,
// the (fake) frontend answers allow, and the gate returns allow with no grant.
func TestApprovalAllowOnce(t *testing.T) {
	c, ids, _ := approvalIDs(t)
	go func() { c.Approve(<-ids, true, false, false) }()

	allow, remember, err := gateApprover{c}.Approve(context.Background(), "bash", "go test", nil)
	if err != nil || !allow || remember {
		t.Fatalf("Approve = (%v,%v,%v), want allow once", allow, remember, err)
	}
}

func TestMemoryApprovalRequestShowsRememberPayload(t *testing.T) {
	approvals := make(chan event.Approval, 1)
	c := newOwnedTestController(t, Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.ApprovalRequest {
			approvals <- e.Approval
		}
	})})

	args := json.RawMessage(`{
		"name": "stable-retrieval-conclusion",
		"description": "History retrieval should reuse stable synthesized conclusions.",
		"type": "feedback",
		"body": "**Why:** repeated history scans are expensive.\n\n**How to apply:** save the stable summary as a memory document."
	}`)
	result := make(chan string, 1)
	go func() {
		allow, _, err := gateApprover{c}.Approve(context.Background(), "remember", "", args)
		if err != nil {
			result <- err.Error()
			return
		}
		if !allow {
			result <- "memory approval denied"
			return
		}
		result <- ""
	}()

	var approval event.Approval
	select {
	case approval = <-approvals:
	case <-time.After(30 * time.Second):
		t.Fatal("memory approval request was not emitted")
	}
	for _, want := range []string{
		`Save/update memory "stable-retrieval-conclusion"`,
		"[feedback]",
		"History retrieval should reuse stable synthesized conclusions.",
		"repeated history scans are expensive",
		"save the stable summary",
	} {
		if !strings.Contains(approval.Subject, want) {
			t.Fatalf("approval subject %q does not contain %q", approval.Subject, want)
		}
	}
	if strings.Contains(approval.Subject, "\n") {
		t.Fatalf("approval subject should be compact for TUI rendering, got %q", approval.Subject)
	}

	c.Approve(approval.ID, true, true, false)
	select {
	case msg := <-result:
		if msg != "" {
			t.Fatalf("Approve returned %s", msg)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("memory approval stayed blocked after Approve")
	}
}

func TestFreshHumanApprovalToolsBypassGuardianAndWaitForUser(t *testing.T) {
	guardianProv := &recordingProvider{
		name:    "guardian",
		streams: [][]provider.Chunk{textTurn(`{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"authorized memory update"}`)},
	}
	guardianSess := guardian.NewSession(guardianProv, tool.NewRegistry(), guardian.PolicyPrompt(), "guardian-test", 0, nil, event.Discard)
	exec := agent.New(&recordingProvider{name: "executor"}, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)

	approvals := make(chan event.Approval, 1)
	c := newOwnedTestController(t, Options{
		Executor: exec,
		Guardian: guardianSess,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				approvals <- e.Approval
			}
		}),
	})

	args := json.RawMessage(`{"name":"prefers-vitest","description":"Preferred test framework","body":"Use vitest for frontend tests."}`)
	type approveResult struct {
		allow    bool
		remember bool
		err      error
	}
	done := make(chan approveResult, 1)
	go func() {
		allow, remember, err := gateApprover{c}.Approve(context.Background(), "remember", "", args)
		done <- approveResult{allow: allow, remember: remember, err: err}
	}()

	var approval event.Approval
	select {
	case approval = <-approvals:
	case <-time.After(30 * time.Second):
		t.Fatal("memory approval request was not emitted after Guardian allow")
	}
	if approval.Tool != "remember" {
		t.Fatalf("approval tool = %q, want remember", approval.Tool)
	}
	if len(guardianProv.requests) != 0 {
		t.Fatalf("guardian reviews = %d, want 0; approval has one authoritative user decision path", len(guardianProv.requests))
	}
	select {
	case got := <-done:
		t.Fatalf("Guardian must not auto-allow remember, got %+v", got)
	case <-time.After(50 * time.Millisecond):
	}

	c.Approve(approval.ID, true, true, false)
	select {
	case got := <-done:
		if got.err != nil || !got.allow || got.remember {
			t.Fatalf("Approve = (%v,%v,%v), want manual allow without remember", got.allow, got.remember, got.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("memory approval stayed blocked after manual Approve")
	}
}

func TestLowRiskProjectMemoryCreateSkipsApprovalPrompt(t *testing.T) {
	store := memory.Store{Dir: t.TempDir()}
	approvals := 0
	c := newOwnedTestController(t, Options{
		Memory: &memory.Set{Store: store},
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				approvals++
			}
		}),
	})
	args := json.RawMessage(`{"name":"release-target","description":"Project release target","type":"project","body":"Release from main."}`)
	allow, remember, reason, err := gateApprover{c}.ApproveWithReason(context.Background(), memoryRememberTool, "", args)
	if err != nil || !allow || remember || reason != "" || approvals != 0 {
		t.Fatalf("safe project create = (%v,%v,%q,%v), approvals=%d", allow, remember, reason, err, approvals)
	}

	out, err := memory.NewRememberTool(store).Execute(memory.WithQueue(context.Background(), c), args)
	if err != nil || !strings.Contains(out, "Saved memory") {
		t.Fatalf("auto-approved remember execution = %q, %v", out, err)
	}
	if got := store.ListAll(); len(got) != 1 || got[0].Name != "release-target" {
		t.Fatalf("saved memories = %+v", got)
	}
}

func TestExistingMemoryRevokesAbandonedAutomaticCreateClaim(t *testing.T) {
	store := memory.Store{Dir: t.TempDir()}
	c := newOwnedTestController(t, Options{Memory: &memory.Set{Store: store}})
	args := json.RawMessage(`{"name":"release-target","description":"Project release target","type":"project","body":"Release from main."}`)

	if assessment := memory.AssessRememberWrite(store, args); !assessment.AutoAllow {
		t.Fatalf("initial assessment = %+v", assessment)
	}
	c.memory.authorizeAutoRemember(args) // approval was issued, then the turn was cancelled
	if _, err := store.Save(memory.Memory{Name: "release-target", Description: "concurrent", Body: "existing"}); err != nil {
		t.Fatal(err)
	}
	if assessment := memory.AssessRememberWrite(store, args); assessment.AutoAllow {
		t.Fatalf("existing assessment = %+v", assessment)
	}
	c.memory.revokeAutoRemember(args)
	if c.ClaimAutoMemoryWrite(args) {
		t.Fatal("abandoned automatic create claim survived an existing-memory reassessment")
	}
}

// TestSessionGrantShortCircuitsGuardianReview: a session grant (or YOLO / the
// approved-plan window) answers an ordinary approval before any guardian review
// or prompt is attempted. Absorbed from PR #6413 by @myipanta.
func TestSessionGrantShortCircuitsGuardianReview(t *testing.T) {
	guardianProv := &recordingProvider{
		name:    "guardian",
		streams: [][]provider.Chunk{textTurn(`{"risk_level":"high","user_authorization":"unknown","outcome":"deny","rationale":"should never run"}`)},
	}
	guardianSess := guardian.NewSession(guardianProv, tool.NewRegistry(), guardian.PolicyPrompt(), "guardian-test", 0, nil, event.Discard)
	exec := agent.New(&recordingProvider{name: "executor"}, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	prompts := 0
	c := newOwnedTestController(t, Options{
		Executor: exec,
		Guardian: guardianSess,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				prompts++
			}
		}),
	})
	subject := approvalDisplaySubject("write_file", "main.go", nil)
	c.approval.grantSession("write_file", subject)

	allow, remember, _, err := gateApprover{c}.ApproveWithReason(context.Background(), "write_file", "main.go", nil)
	if err != nil || !allow || remember {
		t.Fatalf("session-granted approval = (%v,%v,%v), want plain allow", allow, remember, err)
	}
	if len(guardianProv.requests) != 0 || prompts != 0 {
		t.Fatalf("session grant must bypass guardian and prompts, reviews=%d prompts=%d", len(guardianProv.requests), prompts)
	}
}

func TestHeadlessGateRefusesFreshHumanApprovalTools(t *testing.T) {
	gate := NewHeadlessPermissionGate(permission.New("ask", nil, nil, nil))

	for _, toolName := range []string{"remember", "forget"} {
		allow, reason, err := gate.Check(context.Background(), toolName, json.RawMessage(`{}`), false)
		if err != nil || allow || !strings.Contains(reason, "fresh human approval") {
			t.Fatalf("%s headless check = (%v,%q,%v), want fresh-human refusal", toolName, allow, reason, err)
		}
	}

	allow, reason, err := gate.Check(context.Background(), "bash", json.RawMessage(`{"command":"go test ./..."}`), false)
	if err != nil || !allow || reason != "" {
		t.Fatalf("legacy bootstrap ask = (%v,%q,%v), want compatibility allow", allow, reason, err)
	}
}

func TestMemoryApprovalSubjectsAndNotifications(t *testing.T) {
	forgetSubject := approvalDisplaySubject("forget", "", json.RawMessage(`{"name":"wrong-memory"}`))
	if forgetSubject != `Archive memory "wrong-memory"` {
		t.Fatalf("forget approval subject = %q", forgetSubject)
	}
	if got := approvalNotificationText("remember", "Save/update memory with private details"); got != "approval needed: remember" {
		t.Fatalf("remember notification = %q", got)
	}
	if got := approvalNotificationText("forget", `Archive memory "wrong-memory"`); got != "approval needed: forget" {
		t.Fatalf("forget notification = %q", got)
	}
	if got := approvalNotificationText("bash", "go test ./..."); got != "approval needed: bash go test ./..." {
		t.Fatalf("bash notification = %q", got)
	}
	moveSubject := approvalDisplaySubject("move_file", "src/a.md", json.RawMessage(`{"source_path":"src/a.md","destination_path":"docs/a.md"}`))
	if moveSubject != "src/a.md -> docs/a.md" {
		t.Fatalf("move_file approval subject = %q", moveSubject)
	}
}

func TestPermissionRequestHookFiresForToolApproval(t *testing.T) {
	c, ids, payloads := permissionHookController(t, "bash")
	args := json.RawMessage(`{"command":"go test ./..."}`)
	type approveResult struct {
		allow    bool
		remember bool
		err      error
	}
	done := make(chan approveResult, 1)
	go func() {
		allow, remember, err := gateApprover{c}.Approve(context.Background(), "bash", "go test ./...", args)
		done <- approveResult{allow: allow, remember: remember, err: err}
	}()

	id := waitApprovalID(t, ids)
	payload := waitPermissionHook(t, payloads)
	if payload.Event != hook.PermissionRequest {
		t.Fatalf("payload event = %q, want PermissionRequest", payload.Event)
	}
	if payload.ToolName != "bash" {
		t.Fatalf("payload tool = %q, want bash", payload.ToolName)
	}
	if payload.Subject != "go test ./..." {
		t.Fatalf("payload subject = %q, want command subject", payload.Subject)
	}
	if string(payload.ToolArgs) != string(args) {
		t.Fatalf("payload args = %s, want %s", payload.ToolArgs, args)
	}

	c.Approve(id, true, false, false)
	select {
	case got := <-done:
		if got.err != nil || !got.allow || got.remember {
			t.Fatalf("Approve = (%v,%v,%v), want allow once", got.allow, got.remember, got.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("approval stayed blocked")
	}
}

func TestPermissionRequestHookDoesNotFireForPolicyAllow(t *testing.T) {
	c, _, payloads := permissionHookController(t, "bash")
	g := permission.NewGate(permission.New("ask", []string{"bash(go test*)"}, nil, nil), gateApprover{c})

	allow, _, err := g.Check(context.Background(), "bash", json.RawMessage(`{"command":"go test ./..."}`), false)
	if err != nil || !allow {
		t.Fatalf("allow-listed call = (%v,%v), want allowed", allow, err)
	}
	assertNoPermissionHook(t, payloads)
}

func TestPermissionRequestHookDoesNotFireForAutoApprovalMode(t *testing.T) {
	c, _, payloads := permissionHookController(t, "bash")
	c.SetToolApprovalMode(ToolApprovalWorkspaceWrite)
	g := c.newInteractiveGate()

	allow, _, err := g.Check(context.Background(), "bash", json.RawMessage(`{"command":"go test ./..."}`), false)
	if err != nil || !allow {
		t.Fatalf("auto-approved call = (%v,%v), want allowed", allow, err)
	}
	assertNoPermissionHook(t, payloads)
}

func TestPermissionRequestHookDoesNotFireForSessionGrant(t *testing.T) {
	c, _, payloads := permissionHookController(t, "bash")
	c.approval.grantSession("bash", "go test ./...")

	allow, _, err := c.requestApproval(context.Background(), "bash", "go test ./...", nil)
	if err != nil || !allow {
		t.Fatalf("session-granted approval = (%v,%v), want allowed", allow, err)
	}
	assertNoPermissionHook(t, payloads)
}

// TestSessionAuthorizationsCarryAcrossRebuild pins the fix for a rebuild
// (model/effort/profile switch) dropping same-session "Allow for this
// session" tool grants and Plan-mode read-only command trust: only the
// ask/auto/yolo posture string used to survive a controller swap, so a user
// who had already granted a tool this session was asked again after any
// switch.
func TestSessionAuthorizationsCarryAcrossRebuild(t *testing.T) {
	old := newOwnedTestController(t, Options{})
	old.approval.grantSession("bash", "go test ./...")

	fresh := newOwnedTestController(t, Options{})
	fresh.RestoreSessionAuthorizations(old.SessionAuthorizations())

	allow, _, err := fresh.requestApproval(context.Background(), "bash", "go test ./...", nil)
	if err != nil || !allow {
		t.Fatalf("session-granted approval after restore = (%v,%v), want allowed", allow, err)
	}

}

func TestPermissionRequestHookDoesNotFireForYolo(t *testing.T) {
	c, _, payloads := permissionHookController(t, "bash")
	c.SetToolApprovalMode(ToolApprovalDangerFullAccess)

	allow, _, err := c.requestApproval(context.Background(), "bash", "go test ./...", nil)
	if err != nil || !allow {
		t.Fatalf("YOLO approval = (%v,%v), want allowed", allow, err)
	}
	assertNoPermissionHook(t, payloads)
}

func TestPermissionRequestHookDoesNotFireForPlanApproval(t *testing.T) {
	c, ids, payloads := permissionHookController(t, ".*")
	done := make(chan bool, 1)
	errs := make(chan error, 1)
	go func() {
		allow, _, err := c.requestApproval(context.Background(), planApprovalTool, "", nil)
		if err != nil {
			errs <- err
			return
		}
		done <- allow
	}()

	id := waitApprovalID(t, ids)
	assertNoPermissionHook(t, payloads)
	c.Approve(id, true, false, false)

	select {
	case err := <-errs:
		t.Fatalf("plan approval: %v", err)
	case allow := <-done:
		if !allow {
			t.Fatal("manual plan approval should allow")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("plan approval stayed blocked")
	}
}

func TestPermissionRequestHookRedactsMemoryApprovalPayload(t *testing.T) {
	cases := []struct {
		tool string
		args json.RawMessage
	}{
		{
			tool: "remember",
			args: json.RawMessage(`{"name":"private-memory","description":"private description","body":"private memory body"}`),
		},
		{
			tool: "forget",
			args: json.RawMessage(`{"name":"private-memory"}`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			c, ids, payloads := permissionHookController(t, tc.tool)
			done := make(chan string, 1)
			go func() {
				allow, _, err := gateApprover{c}.Approve(context.Background(), tc.tool, "", tc.args)
				if err != nil {
					done <- err.Error()
					return
				}
				if !allow {
					done <- tc.tool + " approval denied"
					return
				}
				done <- ""
			}()

			id := waitApprovalID(t, ids)
			payload := waitPermissionHook(t, payloads)
			if payload.ToolName != tc.tool {
				t.Fatalf("payload tool = %q, want %s", payload.ToolName, tc.tool)
			}
			if payload.Subject != "" {
				t.Fatalf("memory PermissionRequest subject = %q, want redacted", payload.Subject)
			}
			if len(payload.ToolArgs) != 0 {
				t.Fatalf("memory PermissionRequest args = %s, want redacted", payload.ToolArgs)
			}

			c.Approve(id, true, false, false)
			select {
			case msg := <-done:
				if msg != "" {
					t.Fatal(msg)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("memory approval stayed blocked")
			}
		})
	}
}

// TestApprovalDeny confirms a declined call returns allow=false.
func TestApprovalDeny(t *testing.T) {
	c, ids, _ := approvalIDs(t)
	go func() { c.Approve(<-ids, false, false, false) }()

	allow, _, err := gateApprover{c}.Approve(context.Background(), "bash", "rm -rf /", nil)
	if err != nil || allow {
		t.Fatalf("Approve = (%v,%v), want deny", allow, err)
	}
}

// TestApprovalSessionGrantScopesBashToCommand proves an "allow this session"
// answer short-circuits later prompts for the same bash command, but a different
// command still reaches the frontend.
func TestApprovalSessionGrantScopesBashToCommand(t *testing.T) {
	c, ids, prompts := approvalIDs(t)
	go func() {
		c.Approve(<-ids, true, true, false) // grant go build for this session
		c.Approve(<-ids, true, false, false)
	}()

	for i, subject := range []string{"go build", "go build", "go test ./..."} {
		allow, _, err := gateApprover{c}.Approve(context.Background(), "bash", subject, nil)
		if err != nil || !allow {
			t.Fatalf("call %d = (%v,%v), want allow", i, allow, err)
		}
	}
	if *prompts != 2 {
		t.Errorf("prompted %d times, want 2 (same command granted, different command prompts)", *prompts)
	}
}

func TestApprovalSessionGrantCanScopeBashToCommandPrefix(t *testing.T) {
	c, ids, prompts := approvalIDs(t)
	go func() {
		c.Approve(<-ids, true, true, false) // grant bash session (prefix preferred)
		c.Approve(<-ids, true, false, false)
	}()

	for i, subject := range []string{"go test ./...", "go test ./internal/control", "go build ./..."} {
		allow, _, err := gateApprover{c}.Approve(context.Background(), "bash", subject, nil)
		if err != nil || !allow {
			t.Fatalf("call %d = (%v,%v), want allow", i, allow, err)
		}
	}
	if *prompts != 2 {
		t.Errorf("prompted %d times, want 2 (prefix grant should cover similar command only)", *prompts)
	}
}

func TestApprovalRejectsPersistentScopeAndAcceptsSessionScope(t *testing.T) {
	ids := make(chan string, 1)
	c := newOwnedTestController(t, Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				ids <- e.Approval.ID
			}
		}),
	})
	result := make(chan error, 1)
	go func() {
		allow, remember, err := gateApprover{c}.Approve(context.Background(), "bash", "go test ./...", nil)
		if err == nil && (!allow || remember) {
			err = fmt.Errorf("unexpected result allow=%v remember=%v", allow, remember)
		}
		result <- err
	}()
	id := <-ids
	if err := c.approveChecked(id, true, true, true); err == nil || !strings.Contains(err.Error(), "permanent approval") {
		t.Fatalf("persistent approval error = %v", err)
	}
	if err := c.approveChecked(id, true, true, false); err != nil {
		t.Fatalf("session approval: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestApprovalSessionScopeDoesNotPersistRules(t *testing.T) {
	ids := make(chan string, 2)
	prompts := 0
	remembered := false
	c := newOwnedTestController(t, Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				prompts++
				ids <- e.Approval.ID
			}
		}),
		OnRemember: func(rule string) RememberResult {
			remembered = true
			return RememberResult{Rule: rule, Path: "deepzora.toml", Saved: true}
		},
	})
	go func() {
		c.Approve(<-ids, true, true, false)
	}()

	for i := range 2 {
		allow, remember, err := gateApprover{c}.Approve(context.Background(), "bash", "go test ./...", nil)
		if err != nil || !allow || remember {
			t.Fatalf("Approve call %d = (%v,%v,%v), want session authorization", i, allow, remember, err)
		}
	}
	if prompts != 1 {
		t.Fatalf("approval prompts = %d, want one session-scoped prompt", prompts)
	}
	if remembered {
		t.Fatal("session authorization must not persist a permission rule")
	}
}

func TestApprovalSubjectsUseChineseCatalog(t *testing.T) {
	i18n.DetectLanguage("zh")
	t.Cleanup(func() { i18n.DetectLanguage("en") })

	rememberArgs := json.RawMessage(`{"name":"prefers-vitest","type":"user","description":"Preferred test framework","body":"Use Vitest for frontend tests."}`)
	if got := approvalDisplaySubject(memoryRememberTool, "", rememberArgs); !strings.Contains(got, "保存/更新记忆") || !strings.Contains(got, "正文: Use Vitest") {
		t.Fatalf("remember approval subject = %q, want Chinese labels", got)
	}

	forgetArgs := json.RawMessage(`{"name":"old-fact"}`)
	if got := approvalDisplaySubject(memoryForgetTool, "", forgetArgs); got != `归档记忆 "old-fact"` {
		t.Fatalf("forget approval subject = %q, want Chinese archive label", got)
	}
}

func TestApprovalSessionGrantGroupsFileMutationTools(t *testing.T) {
	c, ids, prompts := approvalIDs(t)
	go func() { c.Approve(<-ids, true, true, false) }()

	for i, call := range []struct {
		tool    string
		subject string
	}{
		{"edit_file", "src/a.go"},
		{"write_file", "src/b.go"},
		{"multi_edit", "src/c.go"},
		{"move_file", "src/d.go"},
	} {
		allow, _, err := gateApprover{c}.Approve(context.Background(), call.tool, call.subject, nil)
		if err != nil || !allow {
			t.Fatalf("call %d = (%v,%v), want allow", i, allow, err)
		}
	}
	if *prompts != 1 {
		t.Errorf("prompted %d times, want 1 (file mutation session grant should short-circuit)", *prompts)
	}
}

func TestApprovalSessionGrantKeepsPolicyDenyPrecedence(t *testing.T) {
	c, ids, prompts := approvalIDs(t)
	g := permission.NewGate(permission.New("ask", nil, nil, []string{"bash(rm*)"}), gateApprover{c})
	go func() { c.Approve(<-ids, true, true, false) }()

	allow, _, err := g.Check(context.Background(), "bash", json.RawMessage(`{"command":"go build"}`), false)
	if err != nil || !allow {
		t.Fatalf("first approved call = (%v,%v), want allow", allow, err)
	}
	allow, _, err = g.Check(context.Background(), "bash", json.RawMessage(`{"command":"go build"}`), false)
	if err != nil || !allow {
		t.Fatalf("same-command call after session grant = (%v,%v), want allow", allow, err)
	}
	allow, reason, err := g.Check(context.Background(), "bash", json.RawMessage(`{"command":"rm -rf /tmp/x"}`), false)
	if err != nil || allow || reason == "" {
		t.Fatalf("deny-listed call = (%v,%q,%v), want blocked with reason", allow, reason, err)
	}
	if *prompts != 1 {
		t.Errorf("prompted %d times, want 1", *prompts)
	}
}

// TestApprovalCtxCancel ensures a cancelled turn unblocks the gate with an error
// (rather than hanging) when no one answers.
func TestApprovalCtxCancel(t *testing.T) {
	c := newOwnedTestController(t, Options{Sink: event.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	allow, _, err := gateApprover{c}.Approve(ctx, "bash", "x", nil)
	if err == nil || allow {
		t.Fatalf("Approve on cancelled ctx = (%v,%v), want (false, error)", allow, err)
	}
}

func TestParseRewind(t *testing.T) {
	cps := []checkpoint.Meta{
		{Turn: 0, Prompt: "first"},
		{Turn: 1, Prompt: "second"},
		{Turn: 2, Prompt: "third"},
	}
	cases := []struct {
		args    string
		wantT   int
		wantS   RewindScope
		wantErr bool
	}{
		{"", 2, RewindBoth, false},                       // no args -> latest turn, both
		{"1", 1, RewindBoth, false},                      // turn only
		{"0 code", 0, RewindCode, false},                 // turn + code
		{"1 conversation", 1, RewindConversation, false}, // turn + conversation
		{"2 both", 2, RewindBoth, false},                 // turn + both
		{"abc", 0, RewindBoth, true},                     // invalid turn
		{"0 unknown", 0, RewindBoth, true},               // unknown scope
	}
	for _, tc := range cases {
		t.Run(tc.args, func(t *testing.T) {
			gotT, gotS, err := ParseRewindTarget(tc.args, cps)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseRewind(%q) err=%v, wantErr=%v", tc.args, err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if gotT != tc.wantT || gotS != tc.wantS {
				t.Fatalf("parseRewind(%q) = (%d,%d), want (%d,%d)", tc.args, gotT, gotS, tc.wantT, tc.wantS)
			}
		})
	}
}

func TestParseRewindEmptyCheckpoints(t *testing.T) {
	_, _, err := ParseRewindTarget("", nil)
	if err == nil {
		t.Fatal("expected error when no checkpoints")
	}
}

func TestRunGuardedPanicEmitsTurnDone(t *testing.T) {
	sess := agent.NewSession("sys")
	events := make(chan event.Event, 4)
	c := newOwnedTestController(t, Options{
		Runner: appendingRunner{session: sess},
		Sink:   event.FuncSink(func(e event.Event) { events <- e }),
	})

	go func() {
		c.runGuarded(func(ctx context.Context) error {
			panic("boom")
		})
	}()

	deadline := time.After(30 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Kind != event.TurnDone {
				continue
			}
			if e.Err == nil || !strings.Contains(e.Err.Error(), "boom") {
				t.Fatalf("expected TurnDone.Err to contain panic message, got %v", e.Err)
			}
			goto done
		case <-deadline:
			t.Fatal("timed out waiting for TurnDone after panic")
		}
	}
done:

	waitIdle(t, c)
	if c.Running() {
		t.Fatal("controller still running after panic recovery")
	}
}

// TestRunGuardedParksReplacementUntilTurnDoneReturns pins the finishing-window
// admission contract from both sides: a replacement turn arriving while
// TurnDone is being delivered must NOT start inside the window (the original
// transport-crosstalk guarantee this window exists for), and it must START —
// exactly once — when the window closes. The second half replaces the old
// silent-drop behavior, which lost real input: every caller that submits upon
// seeing turn_done (a frontend's queued auto-send, a bot, a fast Enter) raced
// this window, observed as a CI-flaky lost turn and worked around in
// Composer.tsx by gating auto-send on submitDisabled instead of turn_done.
func TestRunGuardedParksReplacementUntilTurnDoneReturns(t *testing.T) {
	firstTurnDone := make(chan struct{})
	releaseTurnDone := make(chan struct{})
	firstBodyDone := make(chan struct{})
	secondBodyRan := make(chan struct{}, 2)
	var turnDones atomic.Int32
	c := newOwnedTestController(t, Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			if turnDones.Add(1) == 1 {
				close(firstTurnDone)
				<-releaseTurnDone
			}
		}
	})})

	if got := c.runGuarded(func(context.Context) error {
		close(firstBodyDone)
		return nil
	}); got != turnStarted {
		t.Fatalf("first admission = %v, want turnStarted", got)
	}
	<-firstBodyDone
	select {
	case <-firstTurnDone:
	case <-time.After(time.Second):
		t.Fatal("TurnDone delivery did not start")
	}
	if !c.RuntimeStateSnapshot().Running {
		t.Fatal("controller reported idle while TurnDone was still being delivered")
	}
	if got := c.runGuarded(func(context.Context) error {
		secondBodyRan <- struct{}{}
		return nil
	}); got != turnParked {
		t.Fatalf("finishing-window admission = %v, want turnParked", got)
	}
	select {
	case <-secondBodyRan:
		t.Fatal("replacement turn started before TurnDone delivery completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseTurnDone)
	select {
	case <-secondBodyRan:
	case <-time.After(30 * time.Second):
		t.Fatal("parked turn was never started after the finishing window closed")
	}
	deadline := time.Now().Add(30 * time.Second)
	for c.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if c.Running() {
		t.Fatal("controller remained busy after the parked turn completed")
	}
	if got := turnDones.Load(); got != 2 {
		t.Fatalf("TurnDone emitted %d times, want 2 (one per turn)", got)
	}
	select {
	case <-secondBodyRan:
		t.Fatal("parked turn ran more than once")
	default:
	}
}

func TestRunGuardedPanicDoesNotDoubleEmitTurnDone(t *testing.T) {
	sess := agent.NewSession("sys")
	var count atomic.Int32
	events := make(chan event.Event, 8)
	c := newOwnedTestController(t, Options{
		Runner: appendingRunner{session: sess},
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				count.Add(1)
			}
			events <- e
		}),
	})

	go func() {
		c.runGuarded(func(ctx context.Context) error {
			panic("boom")
		})
	}()

	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-events:
			n := count.Load()
			if n >= 1 {
				time.Sleep(50 * time.Millisecond)
				n2 := count.Load()
				if n2 > 1 {
					t.Fatalf("TurnDone emitted %d times, expected 1", n2)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for TurnDone")
		}
	}
}

type blockingRunner struct {
	session *agent.Session
	release chan struct{}
}

func (r blockingRunner) Run(_ context.Context, input string) error {
	r.session.Add(provider.Message{Role: provider.RoleUser, Content: input})
	<-r.release
	return nil
}

func TestRunTurnReportsErrTurnRunning(t *testing.T) {
	sess := agent.NewSession("sys")
	release := make(chan struct{})
	c := newOwnedTestController(t, Options{Runner: blockingRunner{session: sess, release: release}})

	done := make(chan error, 1)
	go func() {
		done <- c.RunTurn(context.Background(), "first")
	}()
	waitForRunning(t, c)

	if err := c.RunTurn(context.Background(), "second"); !errors.Is(err, ErrTurnRunning) {
		t.Fatalf("RunTurn while running error = %v, want ErrTurnRunning", err)
	}

	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first RunTurn returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("first RunTurn did not finish after release")
	}
}

func TestSendWhileRunningDoesNotInterleaveTurns(t *testing.T) {
	sess := agent.NewSession("sys")
	release := make(chan struct{})
	events := make(chan event.Event, 4)
	c := newOwnedTestController(t, Options{
		Runner: blockingRunner{session: sess, release: release},
		Sink: event.FuncSink(func(e event.Event) {
			events <- e
		}),
	})
	defer c.autosaveWG.Wait()

	c.Send("first")
	waitForRunning(t, c)
	c.Send("second")
	close(release)
	waitForTurnDone(t, events)

	var users []string
	for _, m := range sess.Messages {
		if m.Role == provider.RoleUser {
			users = append(users, m.Content)
		}
	}
	if len(users) != 1 || users[0] != "first" {
		t.Fatalf("user turns = %v, want only first turn recorded", users)
	}
}

func waitForRunning(t *testing.T, c *Controller) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.Running() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("controller did not enter running state")
}

func TestMidTurnAutosavePersistsDuringLongTurn(t *testing.T) {
	old := midTurnSnapshotInterval.Load()
	midTurnSnapshotInterval.Store(int64(10 * time.Millisecond))
	defer midTurnSnapshotInterval.Store(old)
	started := make(chan struct{})
	reg := tool.NewRegistry()
	reg.Add(checkpointProbeTool{name: "blocking", started: started})
	prov := agenttestutil.NewMock("test", agenttestutil.Turn{ToolCalls: []provider.ToolCall{{ID: "call", Name: "blocking", Arguments: `{}`}}})
	exec := agent.New(prov, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := newCurrentOwnedTestController(t, Options{Runner: exec, Executor: exec, Sink: event.Discard})
	c.Submit("hello mid-turn persistence")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	ref, _ := c.SessionRef()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := c.SessionService().Query().Snapshot(t.Context(), ref)
		if err == nil && containsUserText(snapshot.Projection.Messages, "hello mid-turn persistence") && snapshot.Projection.ActiveTools["call"] == "blocking" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("admitted turn not durable: %+v, %v", snapshot, err)
		}
		time.Sleep(time.Millisecond)
	}
	c.Cancel()
}

type scriptedRunner struct {
	exec    *agent.Agent
	scripts []func(input string)
}

func (r *scriptedRunner) Run(_ context.Context, input string) error {
	if len(r.scripts) == 0 {
		return nil
	}
	next := r.scripts[0]
	r.scripts = r.scripts[1:]
	next(input)
	return nil
}

func TestApprovedPlanAutoApproveEndsWithExecutionTurn(t *testing.T) {
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	runner := &scriptedRunner{exec: exec}

	var c *Controller
	approvalPrompts := 0
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind != event.ApprovalRequest {
			return
		}
		approvalPrompts++
		if e.Approval.Tool == planApprovalTool {
			go c.Approve(e.Approval.ID, true, false, false)
			return
		}
		go c.Approve(e.Approval.ID, false, false, false)
	})
	c = newOwnedTestController(t, Options{Runner: runner, Executor: exec, Sink: sink})
	c.SetPlanMode(true)

	runner.scripts = append(runner.scripts,
		func(input string) {
			exec.Session().Add(provider.Message{Role: provider.RoleAssistant, Content: "1. Create the file\n2. Update the file"})
		},
		func(input string) {
			if input != planApprovedMessage {
				t.Fatalf("approved execution input = %q, want planApprovedMessage", input)
			}
			exec.Session().Add(provider.Message{Role: provider.RoleAssistant, Content: "first step done; paused for review", ToolCalls: []provider.ToolCall{{
				ID: "todo-1", Name: "todo_write", Arguments: `{"todos":[{"content":"Create the file","status":"completed"},{"content":"Update the file","status":"in_progress"}]}`,
			}}})
		},
	)

	if err := c.runTurn(context.Background(), "plan this"); err != nil {
		t.Fatal(err)
	}
	if approvalPrompts != 1 {
		t.Fatalf("approval prompts after plan = %d, want 1", approvalPrompts)
	}

	// The plan approval auto-approves writers for the execution turn only. A later
	// turn does not inherit it, and "继续" carries no special meaning — Compose must
	// not inject any marker, and the next writer falls back to per-tool approval.
	if got := c.Compose("继续"); StripComposePrefixes(got) != "继续" {
		t.Fatalf("a paused approved plan must not marker-prefix the next turn, got %q", got)
	}
	allow, _, err := gateApprover{c}.Approve(context.Background(), "write_file", "/tmp/a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if allow {
		t.Fatal("writer after the execution turn should return to per-tool approval, not auto-allow")
	}
	if approvalPrompts != 2 {
		t.Fatalf("writer after the execution turn should prompt, prompts=%d", approvalPrompts)
	}
}

func TestApprovedPlanDoesNotAutoApproveNonContinuationTurn(t *testing.T) {
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	runner := &scriptedRunner{exec: exec}

	var c *Controller
	approvalPrompts := 0
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind != event.ApprovalRequest {
			return
		}
		approvalPrompts++
		if e.Approval.Tool == planApprovalTool {
			go c.Approve(e.Approval.ID, true, false, false)
			return
		}
		go c.Approve(e.Approval.ID, false, false, false)
	})
	c = newOwnedTestController(t, Options{Runner: runner, Executor: exec, Sink: sink})
	c.SetPlanMode(true)

	runner.scripts = append(runner.scripts,
		func(input string) {
			exec.Session().Add(provider.Message{Role: provider.RoleAssistant, Content: "1. Create the file\n2. Update the file"})
		},
		func(input string) {
			exec.Session().Add(provider.Message{Role: provider.RoleAssistant, Content: "paused", ToolCalls: []provider.ToolCall{{
				ID: "todo-1", Name: "todo_write", Arguments: `{"todos":[{"content":"Create the file","status":"completed"},{"content":"Update the file","status":"in_progress"}]}`,
			}}})
		},
	)

	if err := c.runTurn(context.Background(), "plan this"); err != nil {
		t.Fatal(err)
	}
	if got := c.Compose("先别继续"); StripComposePrefixes(got) != "先别继续" {
		t.Fatalf("non-continuation input should not be marker-prefixed, got %q", got)
	}

	allow, _, err := gateApprover{c}.Approve(context.Background(), "write_file", "/tmp/a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if allow {
		t.Fatal("non-continuation turn should not inherit approved-plan auto approval")
	}
	if approvalPrompts != 2 {
		t.Fatalf("non-continuation writer should prompt after plan approval, prompts=%d", approvalPrompts)
	}
}

// writeCmdFile creates a command .md file with frontmatter under dir.
func writeCmdFile(t *testing.T, dir, name, description, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---\ndescription: %s\n---\n%s\n", description, body)
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCommandsAtomicPointer verifies that commands passed via Options.Commands are
// correctly exposed through the atomic-pointer Commands() getter and that
// CustomCommand resolves and renders them. Missing commands return found=false.
func TestCommandsAtomicPointer(t *testing.T) {
	cmds := []command.Command{
		{Name: "review", Description: "Review code", Body: "Review $1"},
		{Name: "test", Description: "Run tests", Body: "Test $1"},
	}
	c := newOwnedTestController(t, Options{
		Commands: cmds,
		Sink:     &typedNilControllerSink{},
		Registry: tool.NewRegistry(),
	})

	// Commands() returns what was passed via Options
	got := c.Commands()
	if len(got) != 2 {
		t.Fatalf("Commands() = %d, want 2", len(got))
	}
	// Check retrieval via CustomCommand
	sent, ok := c.CustomCommand("/review myfile.go")
	if !ok {
		t.Error("/review should be found")
	}
	if !strings.Contains(sent, "Review myfile.go") {
		t.Errorf("unexpected render: %q", sent)
	}
	_, ok = c.CustomCommand("/missing")
	if ok {
		t.Error("/missing should not be found")
	}

	// Commands() getter uses atomic.Pointer internally
	if cmds2 := c.Commands(); len(cmds2) != 2 {
		t.Errorf("Commands() = %d after change, want 2", len(cmds2))
	}
}

// TestReloadCommandsFromFilesystem exercises ReloadCommands against real .md
// files in a temp workspace: initial load, hot-reload with a new file, and
// hot-reload after modifying an existing file. Also verifies that skills are
// preserved across the reload.
func TestReloadCommandsFromFilesystem(t *testing.T) {
	// Isolate HOME so CommandDirsForRoot does not pick up global .md command files.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))

	wsRoot := t.TempDir()
	cmdDir := filepath.Join(wsRoot, ".deepzora", "commands")
	writeCmdFile(t, cmdDir, "review", "Review code", "Review $1")
	writeCmdFile(t, cmdDir, "test", "Run tests", "Test $1")

	// Create a minimal in-memory skill to verify skills are preserved across reload.
	sk := skill.Skill{
		Name:        "myskill",
		Description: "Test skill",
		Body:        "You are a test skill. User says: {{.Input}}",
	}

	reg := tool.NewRegistry()
	c := newOwnedTestController(t, Options{
		Sink:          &typedNilControllerSink{},
		Registry:      reg,
		WorkspaceRoot: wsRoot,
		Skills:        []skill.Skill{sk},
	})

	// ReloadCommands should pick up the two .md files and preserve the skill.
	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("ReloadCommands: %v", err)
	}
	cmds := c.Commands()
	if len(cmds) != 2 {
		t.Fatalf("Commands() = %d after reload, want 2", len(cmds))
	}

	// CustomCommand should resolve through the hot-swapped getter
	sent, ok := c.CustomCommand("/review hello.go")
	if !ok {
		t.Fatal("/review should be found after reload")
	}
	if !strings.Contains(sent, "Review hello.go") {
		t.Errorf("render = %q, want Review hello.go", sent)
	}

	// Missing command still not found
	if _, ok := c.CustomCommand("/nope"); ok {
		t.Error("/nope should not be found")
	}

	// Skill should appear in the slash_command tool's description after reload.
	if tool, found := reg.Get("slash_command"); found {
		if !strings.Contains(tool.Description(), "myskill") {
			t.Error("skill 'myskill' should appear in slash_command tool Description after ReloadCommands")
		}
	} else {
		t.Error("slash_command tool should be registered after ReloadCommands")
	}

	// Skill should still be callable via RunSkill after reload.
	if _, ok := c.RunSkill("/myskill"); !ok {
		t.Error("RunSkill(/myskill) should find the skill after ReloadCommands")
	}

	// Hot-reload: add a new command file
	writeCmdFile(t, cmdDir, "count", "Count to N", "Count from 1 to $1")
	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("ReloadCommands (add): %v", err)
	}
	if cmds := c.Commands(); len(cmds) != 3 {
		t.Fatalf("Commands() = %d after add, want 3", len(cmds))
	}

	// Hot-reload: modify an existing command
	writeCmdFile(t, cmdDir, "review", "Review code (friendly)", "Kindly review $1")
	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("ReloadCommands (modify): %v", err)
	}
	if cmds := c.Commands(); len(cmds) != 3 {
		t.Fatalf("Commands() = %d after modify, want 3", len(cmds))
	}
	sent, ok = c.CustomCommand("/review world.go")
	if !ok {
		t.Fatal("/review should be found after modify")
	}
	if !strings.Contains(sent, "Kindly review world.go") {
		t.Errorf("render after modify = %q, want Kindly review world.go", sent)
	}

	// The slash_command tool should be registered and updated
	if _, found := reg.Get("slash_command"); !found {
		t.Error("slash_command tool should be registered after ReloadCommands")
	}
}

// TestReloadCommandsDeleteFile verifies that removing a command .md file and
// reloading causes the command to disappear from both Commands() and
// CustomCommand(), while other commands remain intact.
func TestReloadCommandsDeleteFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))

	wsRoot := t.TempDir()
	cmdDir := filepath.Join(wsRoot, ".deepzora", "commands")
	writeCmdFile(t, cmdDir, "alpha", "Alpha cmd", "Alpha $1")
	writeCmdFile(t, cmdDir, "beta", "Beta cmd", "Beta $1")

	reg := tool.NewRegistry()
	c := newOwnedTestController(t, Options{
		Sink:          &typedNilControllerSink{},
		Registry:      reg,
		WorkspaceRoot: wsRoot,
	})

	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	if got := len(c.Commands()); got != 2 {
		t.Fatalf("Commands() = %d, want 2", got)
	}

	// Delete alpha.md
	if err := os.Remove(filepath.Join(cmdDir, "alpha.md")); err != nil {
		t.Fatal(err)
	}

	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("reload after delete: %v", err)
	}
	if got := len(c.Commands()); got != 1 {
		t.Fatalf("Commands() = %d after delete, want 1", got)
	}

	// /alpha should no longer be found
	if _, ok := c.CustomCommand("/alpha x"); ok {
		t.Error("/alpha should NOT be found after deletion")
	}
	// /beta should still work
	sent, ok := c.CustomCommand("/beta y")
	if !ok {
		t.Error("/beta should still be found")
	}
	if !strings.Contains(sent, "Beta y") {
		t.Errorf("render = %q, want Beta y", sent)
	}
}

// TestReloadCommandsMalformedFile verifies that a malformed .md file causes
// ReloadCommands to return an error but does not prevent other valid commands
// from loading.
func TestReloadCommandsMalformedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))

	wsRoot := t.TempDir()
	cmdDir := filepath.Join(wsRoot, ".deepzora", "commands")
	writeCmdFile(t, cmdDir, "good", "Good cmd", "Good $1")

	// Write a malformed file (no valid frontmatter)
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(cmdDir, "broken.md")
	if err := os.WriteFile(broken, []byte("this is not valid yaml\n---\nrandom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	c := newOwnedTestController(t, Options{
		Sink:          &typedNilControllerSink{},
		Registry:      reg,
		WorkspaceRoot: wsRoot,
	})

	err := c.ReloadCommands(context.Background())
	// We expect some error from the malformed file
	if err == nil {
		t.Log("ReloadCommands returned nil error despite malformed file — command.Load may tolerate it")
	} else {
		t.Logf("ReloadCommands returned error (expected): %v", err)
	}

	// The valid command should still be loadable
	cmds := c.Commands()
	foundGood := false
	for _, cmd := range cmds {
		if cmd.Name == "good" {
			foundGood = true
		}
	}
	if !foundGood {
		t.Errorf("valid command 'good' should be present, got commands: %v", cmdNames(cmds))
	}
}

// TestReloadCommandsSameNameAcrossDirs verifies that when the same command
// name exists in multiple convention directories, the later-scanned directory
// (higher priority) wins. ConventionDirs = [".deepzora", ".agents", ".agent",
// ".claude"], scanned in reverse, so .deepzora is highest priority.
func TestReloadCommandsSameNameAcrossDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))

	wsRoot := t.TempDir()

	// Lower priority: .claude/commands
	claudeDir := filepath.Join(wsRoot, ".claude", "commands")
	writeCmdFile(t, claudeDir, "greet", "Claude greet", "Hello from Claude: $1")

	// Higher priority: .deepzora/commands
	deepzoraDir := filepath.Join(wsRoot, ".deepzora", "commands")
	writeCmdFile(t, deepzoraDir, "greet", "Deepzora greet", "Hello from Deepzora: $1")

	reg := tool.NewRegistry()
	c := newOwnedTestController(t, Options{
		Sink:          &typedNilControllerSink{},
		Registry:      reg,
		WorkspaceRoot: wsRoot,
	})

	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// There should be exactly 1 command named "greet"
	cmds := c.Commands()
	count := 0
	for _, cmd := range cmds {
		if cmd.Name == "greet" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 'greet' command, got %d", count)
	}

	// The winning version should be from .deepzora (highest priority)
	sent, ok := c.CustomCommand("/greet world")
	if !ok {
		t.Fatal("/greet should be found")
	}
	if !strings.Contains(sent, "Hello from Deepzora") {
		t.Errorf("expected .deepzora version to win, got render: %q", sent)
	}
}

func TestReloadCommandsUsesCanonicalPluginNameAlongsideProjectShortName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	deepzoraHome := filepath.Join(home, ".deepzora")
	t.Setenv("DEEPZORA_HOME", deepzoraHome)

	pluginRoot := filepath.Join(deepzoraHome, "plugins", "pwf")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, pluginpkg.ClaudeManifest), []byte(`{"name":"pwf"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCmdFile(t, filepath.Join(pluginRoot, "commands"), "plan", "Plugin plan", "PLUGIN $1")
	writeCmdFile(t, filepath.Join(pluginRoot, "commands"), "status", "Plugin status", "STATUS $1")
	if err := pluginpkg.Upsert(deepzoraHome, pluginpkg.InstalledPlugin{Name: "pwf", Root: "plugins/pwf", ManifestKind: "claude", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	writeCmdFile(t, filepath.Join(workspace, ".deepzora", "commands"), "plan", "Project plan", "PROJECT $1")
	c := newOwnedTestController(t, Options{Sink: &typedNilControllerSink{}, Registry: tool.NewRegistry(), WorkspaceRoot: workspace})
	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("ReloadCommands: %v", err)
	}

	if got, ok := c.CustomCommand("/plan task"); !ok || got != "PROJECT task" {
		t.Fatalf("short command = %q, %v; want project winner", got, ok)
	}
	if got, ok := c.CustomCommand("/pwf:plan task"); !ok || got != "PLUGIN task" {
		t.Fatalf("qualified plugin command = %q, %v", got, ok)
	}
	if got, ok := c.CustomCommand("/status now"); ok || got != "" {
		t.Fatalf("unexpected unqualified plugin command = %q, %v", got, ok)
	}
	if got, ok := c.CustomCommand("/pwf:status now"); !ok || got != "STATUS now" {
		t.Fatalf("canonical plugin status command = %q, %v", got, ok)
	}
	cmds := c.Commands()
	canonicalFound := false
	hiddenFound := false
	for _, cmd := range cmds {
		if cmd.Name == "pwf:plan" && cmd.Plugin == "pwf" && cmd.ShortName == "plan" {
			canonicalFound = true
		}
		if cmd.Name == "status" && cmd.Plugin == "pwf" && cmd.ShortName == "status" {
			hiddenFound = true
		}
	}
	if !canonicalFound || hiddenFound {
		t.Fatalf("plugin command metadata missing: %+v", cmds)
	}
}

// TestReloadCommandsEmptySet verifies that deleting all command files and
// reloading results in an empty Commands() slice, while the slash_command tool
// still exists in the Registry (containing only Skills).
func TestReloadCommandsEmptySet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))

	wsRoot := t.TempDir()
	cmdDir := filepath.Join(wsRoot, ".deepzora", "commands")
	writeCmdFile(t, cmdDir, "temp", "Temp cmd", "Temp $1")

	sk := skill.Skill{
		Name:        "preserved",
		Description: "A skill to keep",
		Body:        "Skill body: {{.Input}}",
	}

	reg := tool.NewRegistry()
	c := newOwnedTestController(t, Options{
		Sink:          &typedNilControllerSink{},
		Registry:      reg,
		WorkspaceRoot: wsRoot,
		Skills:        []skill.Skill{sk},
	})

	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	if got := len(c.Commands()); got != 1 {
		t.Fatalf("Commands() = %d, want 1", got)
	}

	// Delete all command files
	if err := os.Remove(filepath.Join(cmdDir, "temp.md")); err != nil {
		t.Fatal(err)
	}

	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("reload after delete all: %v", err)
	}
	if got := len(c.Commands()); got != 0 {
		t.Fatalf("Commands() = %d after delete all, want 0", got)
	}

	// /temp should no longer be found
	if _, ok := c.CustomCommand("/temp x"); ok {
		t.Error("/temp should NOT be found after deletion")
	}

	// slash_command tool should still exist (for Skills)
	slashTool, found := reg.Get("slash_command")
	if !found {
		t.Fatal("slash_command tool should still exist even with 0 commands")
	}
	// It should still contain the skill
	if !strings.Contains(slashTool.Description(), "preserved") {
		t.Error("skill 'preserved' should still appear in slash_command tool Description")
	}
}

// TestReloadCommandsDesktopManagementNotice verifies the desktop/HTTP path:
// when the frontend submits "/reload-cmd" as raw input, Submit → managementNotice
// handles it and emits a Notice event with the correct count.
func TestReloadCommandsDesktopManagementNotice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))

	wsRoot := t.TempDir()
	cmdDir := filepath.Join(wsRoot, ".deepzora", "commands")
	writeCmdFile(t, cmdDir, "hello", "Greet", "Hello $1")
	writeCmdFile(t, cmdDir, "review", "Review code", "Review $1")

	var notices []string
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			notices = append(notices, e.Text)
		}
	})

	reg := tool.NewRegistry()
	c := newOwnedTestController(t, Options{
		Sink:          sink,
		Registry:      reg,
		WorkspaceRoot: wsRoot,
	})

	// Initial load so Commands() is populated.
	if err := c.ReloadCommands(context.Background()); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	notices = nil // reset

	// Desktop path: managementNotice("/reload-cmd") should emit a notice.
	handled := c.managementNotice("/reload-cmd")
	if !handled {
		t.Fatal("managementNotice(/reload-cmd) should return true")
	}
	if len(notices) != 1 {
		t.Fatalf("expected 1 notice, got %d: %v", len(notices), notices)
	}
	if !strings.Contains(notices[0], "commands reloaded") {
		t.Errorf("notice = %q, want 'commands reloaded'", notices[0])
	}
	if !strings.Contains(notices[0], "2 available") {
		t.Errorf("notice = %q, want '2 available'", notices[0])
	}

	// Delete one command file and reload again.
	if err := os.Remove(filepath.Join(cmdDir, "hello.md")); err != nil {
		t.Fatal(err)
	}
	notices = nil
	handled = c.managementNotice("/reload-cmd")
	if !handled {
		t.Fatal("managementNotice(/reload-cmd) after delete should return true")
	}
	if len(notices) != 1 {
		t.Fatalf("expected 1 notice after delete, got %d: %v", len(notices), notices)
	}
	if !strings.Contains(notices[0], "1 available") {
		t.Errorf("notice after delete = %q, want '1 available'", notices[0])
	}

	// Delete all and verify empty-set notice.
	if err := os.Remove(filepath.Join(cmdDir, "review.md")); err != nil {
		t.Fatal(err)
	}
	notices = nil
	handled = c.managementNotice("/reload-cmd")
	if !handled {
		t.Fatal("managementNotice(/reload-cmd) empty set should return true")
	}
	if len(notices) != 1 {
		t.Fatalf("expected 1 notice for empty set, got %d: %v", len(notices), notices)
	}
	if !strings.Contains(notices[0], "0 available") {
		t.Errorf("notice for empty set = %q, want '0 available'", notices[0])
	}
}

// cmdNames is a test helper that extracts command names from a slice.
func cmdNames(cmds []command.Command) []string {
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name
	}
	return names
}
