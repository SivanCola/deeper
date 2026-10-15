package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

type desktopSessionTitleProvider struct {
	started chan struct{}
	chunks  chan provider.Chunk
	request provider.Request
	// budget is what was left of the model round trip's deadline when the
	// request reached the provider, and streamedAt when that happened.
	budget     time.Duration
	streamedAt time.Time
}

func (p *desktopSessionTitleProvider) Name() string { return "desktop-session-title" }

func (p *desktopSessionTitleProvider) Stream(ctx context.Context, request provider.Request) (<-chan provider.Chunk, error) {
	p.request = request
	p.streamedAt = time.Now()
	if deadline, ok := ctx.Deadline(); ok {
		p.budget = time.Until(deadline)
	}
	if p.started != nil {
		close(p.started)
	}
	return p.chunks, nil
}

func TestAIRenameCanonicalSessionUsesDurableHistoryInsteadOfEmptyLegacyFile(t *testing.T) {
	for _, identity := range []string{"topic", "session-ref", "session-route"} {
		t.Run(identity, func(t *testing.T) {
			app, ctrl, runtime, prov, path := newCanonicalTitleFixture(t)
			appendSessionTestMessage(t, runtime, "host", provider.Message{ID: "host", Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "hidden host policy"})
			appendSessionTestMessage(t, runtime, "user", provider.Message{ID: "user", Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "wrapped model input", RawContent: "帮我制作扫雷游戏"})
			// Long tool work pushes the authored turn outside the recent window.
			for i := range 110 {
				id := fmt.Sprintf("tool-%d", i)
				appendSessionTestMessage(t, runtime, id, provider.Message{ID: id, Role: provider.RoleTool, Content: "tool output"})
			}
			// Compaction changes model context without erasing the UI transcript.
			if _, err := runtime.Session().AppendBatch(t.Context(), "compact", []session.Event{{Kind: "model/context-replace", Payload: []byte(`{"messages":[{"role":"assistant","content":"compacted summary"}],"reason":"compaction"}`)}}); err != nil {
				t.Fatal(err)
			}
			selector := SessionSelector{TopicID: "topic-canonical"}
			if identity != "topic" {
				app.tabs["test"].TopicID = ""
				ref := runtime.Ref()
				selector = SessionSelector{Ref: &ref}
				if identity == "session-route" {
					selector = SessionSelector{SessionPath: sessionRoute(ref.SessionID)}
				}
			}
			result, err := app.AIRenameSessionTarget(selector)
			if err != nil || result.Title != "制作扫雷游戏" {
				t.Fatalf("AIRenameSessionTarget = %+v, %v", result, err)
			}
			if got, err := ctrl.SessionService().Query().Stat(t.Context(), runtime.Ref()); err != nil || got.Title != result.Title {
				t.Fatalf("canonical title = %+v, %v", got, err)
			}
			if got := prov.request.Messages; len(got) != 2 || got[1].Content != "帮我制作扫雷游戏" {
				t.Fatalf("title prompt = %+v", got)
			}
			if bytes, err := os.ReadFile(path); err != nil || len(bytes) != 0 {
				t.Fatalf("legacy file changed: length=%d, err=%v", len(bytes), err)
			}
			if meta, ok, err := agent.LoadBranchMeta(path); err != nil || (ok && meta.CustomTitle != "") {
				t.Fatalf("canonical rename wrote legacy title: %+v, %v", meta, err)
			}
			if identity == "topic" {
				if got := loadTopicTitle("", selector.TopicID); got == result.Title || app.tabs["test"].TopicTitle != result.Title {
					t.Fatalf("sidebar title = %q, runtime title = %q", got, app.tabs["test"].TopicTitle)
				}
			}
		})
	}
}

// The model round trip owns the AI-title budget: control bounds that call at
// sessionTitleTimeout, measured from the call. A second host-level deadline
// over the whole operation would spend part of it on durable preparation —
// the snapshot, the flush and the history projection TitleMessages builds —
// so a long conversation on a slow host hands the model a short budget and
// eventually loses an already generated title to a deadline that belongs to
// the provider, reported as the opaque operation_failed.
func TestAISessionTitleBudgetBelongsToTheModelRoundTrip(t *testing.T) {
	// control.sessionTitleTimeout. The host must hand over all of it.
	const modelRoundTripBudget = 30 * time.Second
	app, _, runtime, prov, _ := newCanonicalTitleFixture(t)
	appendSessionTestMessage(t, runtime, "user", provider.Message{
		ID: "user", Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content: "wrapped model input", RawContent: "帮我制作扫雷游戏",
	})
	// Enough durable history that its first projection build is measurable.
	for i := range 60 {
		id := fmt.Sprintf("tool-%d", i)
		appendSessionTestMessage(t, runtime, id, provider.Message{ID: id, Role: provider.RoleTool, Content: "tool output"})
	}

	started := time.Now()
	result, err := app.AIRenameSessionTarget(SessionSelector{TopicID: "topic-canonical"})
	if err != nil || result.Title != "制作扫雷游戏" {
		t.Fatalf("AIRenameSessionTarget = %+v, %v", result, err)
	}

	if prov.streamedAt.IsZero() {
		t.Fatal("the model was never asked for a title")
	}
	if prov.budget < modelRoundTripBudget-time.Second {
		t.Fatalf("durable preparation of %v left the model only %v of its %v budget",
			prov.streamedAt.Sub(started), prov.budget, modelRoundTripBudget)
	}
}

func TestAIRenameDoesNotRenameSiblingCreatedDuringGeneration(t *testing.T) {
	app, _, runtime, prov, _ := newCanonicalTitleFixture(t)
	appendSessionTestMessage(t, runtime, "user", provider.Message{ID: "user", Role: provider.RoleUser, Content: "rename only A"})
	prov.started, prov.chunks = make(chan struct{}), make(chan provider.Chunk, 2)
	done := make(chan error, 1)
	go func() {
		_, err := app.AIRenameSessionTarget(SessionSelector{SessionPath: sessionRoute(runtime.Ref().SessionID)})
		done <- err
	}()
	select {
	case <-prov.started:
	case err := <-done:
		t.Fatalf("early result: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not start")
	}
	app.mu.Lock()
	app.tabs["sibling"] = &WorkspaceTab{ID: "sibling", Scope: "global", TopicID: "topic-canonical", TopicTitle: "B unchanged", SessionID: "sibling"}
	app.mu.Unlock()
	prov.chunks <- provider.Chunk{Type: provider.ChunkText, Text: "A changed"}
	prov.chunks <- provider.Chunk{Type: provider.ChunkDone}
	close(prov.chunks)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if app.tabs["sibling"].TopicTitle != "B unchanged" {
		t.Fatal("late AI rename changed sibling title")
	}
}

func TestAIRenameCanonicalSessionDoesNotRequireTargetTab(t *testing.T) {
	app, _, runtime, prov, path := newCanonicalTitleFixture(t)
	appendSessionTestMessage(t, runtime, "user", provider.Message{
		ID: "user", Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "rename a cold sidebar session",
	})
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "global", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspaceID, runtime.Ref().SessionID, ""); err != nil {
		t.Fatal(err)
	}
	if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), runtime.Ref().SessionID, "cold-topic", "Cold topic"); err != nil {
		t.Fatal(err)
	}

	// Keep another conversation active only as the provider host. The target
	// has no tab/controller binding and must be resolved from durable identity.
	generator := newDesktopSessionTitleController(filepath.Dir(path), path, prov)
	t.Cleanup(generator.Close)
	app.mu.Lock()
	app.tabs["test"].Ctrl = generator
	app.tabs["test"].TopicID = "active-other-topic"
	app.tabs["test"].SessionID = "active-other-session"
	app.mu.Unlock()

	result, err := app.AIRenameSessionTarget(SessionSelector{SessionPath: sessionRoute(runtime.Ref().SessionID)})
	if err != nil || result.Title != "制作扫雷游戏" {
		t.Fatalf("AIRenameSessionTarget = %+v, %v", result, err)
	}
	if app.tabs["test"].TopicID != "active-other-topic" || app.tabs["test"].SessionID != "active-other-session" {
		t.Fatal("AI rename navigated away from the active conversation")
	}
	info, err := app.desktopSessionService("").Query().Stat(t.Context(), runtime.Ref())
	if err != nil || info.Title != result.Title {
		t.Fatalf("cold target title = %+v, %v", info, err)
	}
}

func TestAIRenameCanonicalEmptySessionReturnsProductError(t *testing.T) {
	app, _, _, _, _ := newCanonicalTitleFixture(t)
	if _, err := app.AIRenameSessionTarget(SessionSelector{TopicID: "topic-canonical"}); err == nil || !strings.Contains(err.Error(), "session_operation:no_messages:") {
		t.Fatalf("empty session error = %v", err)
	}
}

func TestAIRenameSessionDeduplicatesSameTarget(t *testing.T) {
	app, _, runtime, prov, _ := newCanonicalTitleFixture(t)
	appendSessionTestMessage(t, runtime, "user", provider.Message{
		ID: "user", Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "deduplicate this rename",
	})
	prov.started = make(chan struct{})
	prov.chunks = make(chan provider.Chunk, 2)
	first := make(chan error, 1)
	go func() {
		_, err := app.AIRenameSessionTarget(SessionSelector{TopicID: "topic-canonical"})
		first <- err
	}()
	<-prov.started
	if _, err := app.AIRenameSessionTarget(SessionSelector{TopicID: "topic-canonical"}); err == nil || !strings.Contains(err.Error(), "session_operation:operation_busy:") {
		t.Fatalf("duplicate rename error = %v", err)
	}
	prov.chunks <- provider.Chunk{Type: provider.ChunkText, Text: "Deduplicated title"}
	prov.chunks <- provider.Chunk{Type: provider.ChunkDone}
	close(prov.chunks)
	if err := <-first; err != nil {
		t.Fatalf("first rename: %v", err)
	}
}

func TestAISessionTitleOldFinallyCannotClearNewOperation(t *testing.T) {
	app := NewApp()
	_, oldCancel := context.WithCancelCause(context.Background())
	defer oldCancel(context.Canceled)
	_, newCancel := context.WithCancelCause(context.Background())
	defer newCancel(context.Canceled)
	const key = "path:/session"
	app.aiSessionTitleInFlight[key] = aiSessionTitleOperation{ID: "old", Cancel: oldCancel}
	app.cancelAISessionTitle(key)
	app.aiSessionTitleInFlight[key] = aiSessionTitleOperation{ID: "new", Cancel: newCancel}
	app.finishAISessionTitle(key, "old")
	if got := app.aiSessionTitleInFlight[key].ID; got != "new" {
		t.Fatalf("old finally cleared operation %q, want new", got)
	}
	app.finishAISessionTitle(key, "new")
	if _, ok := app.aiSessionTitleInFlight[key]; ok {
		t.Fatal("owning finally did not clear completed operation")
	}
}

func TestInvalidateAuxiliaryProviderOperationsCancelsAndFencesRequests(t *testing.T) {
	app := NewApp()
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(context.Canceled) })
	app.aiSessionTitleInFlight["path:/cold"] = aiSessionTitleOperation{ID: "old", Cancel: cancel}
	before := app.auxiliaryProviderGeneration.Load()

	app.invalidateAuxiliaryProviderOperations()

	if app.auxiliaryProviderGeneration.Load() != before+1 {
		t.Fatalf("auxiliary provider generation did not advance")
	}
	if len(app.aiSessionTitleInFlight) != 0 {
		t.Fatalf("invalidated operations remain in flight: %+v", app.aiSessionTitleInFlight)
	}
	var operationErr *SessionOperationError
	if !errors.As(context.Cause(ctx), &operationErr) || operationErr.Code != "provider_unavailable" {
		t.Fatalf("cancellation cause = %v, want provider_unavailable", context.Cause(ctx))
	}
}

func newCanonicalTitleFixture(t *testing.T) (*App, *control.Controller, *session.Runtime, *desktopSessionTitleProvider, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	service := app.desktopSessionService(dir)
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "canonical-title", CWD: globalWorkspaceRoot(), Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	path := agent.NewSessionPath(dir, "legacy-empty")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	chunks := make(chan provider.Chunk, 2)
	chunks <- provider.Chunk{Type: provider.ChunkText, Text: "制作扫雷游戏"}
	chunks <- provider.Chunk{Type: provider.ChunkDone}
	close(chunks)
	prov := &desktopSessionTitleProvider{chunks: chunks}
	// The cold path uses real config/resolver assembly and a disposable HTTP
	// provider, not a controller borrowed from another conversation.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if prov.started != nil {
			close(prov.started)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for {
			select {
			case <-r.Context().Done():
				return
			case chunk, ok := <-prov.chunks:
				if !ok || chunk.Type == provider.ChunkDone {
					fmt.Fprint(w, "data: [DONE]\n\n")
					return
				}
				body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": chunk.Text}}}})
				fmt.Fprintf(w, "data: %s\n\n", body)
				w.(http.Flusher).Flush()
			}
		}
	}))
	t.Cleanup(server.Close)
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.DefaultModel = "test/title-model"
	cfg.Providers = []config.ProviderEntry{{Models: []string{"title-model"}, Default: "title-model", Name: "test", Kind: "openai", Model: "title-model", BaseURL: server.URL, NoProxy: true}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	workspace, err := app.ensureDesktopWorkspace(t.Context(), "global", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspace, runtime.Ref().SessionID, ""); err != nil {
		t.Fatal(err)
	}
	if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), runtime.Ref().SessionID, "topic-canonical", ""); err != nil {
		t.Fatal(err)
	}
	ctrl := control.New(control.Options{
		SessionDir: dir, SessionPath: path, ModelRef: "test/title-model",
		SessionService: service, SessionRuntime: runtime, ExclusiveSession: true,
		ProviderResolver: &provider.StaticResolver{Descriptors: []provider.Descriptor{{Ref: "test/title-model"}}, Providers: map[string]provider.Provider{"test/title-model": prov}},
	})
	t.Cleanup(ctrl.Close)
	installDesktopSessionTitleTab(app, ctrl, "topic-canonical", path)
	app.tabs["test"].SessionID = runtime.Ref().SessionID
	app.tabs["test"].SessionWorkspace.ID = workspace
	if _, _, err := app.ensureSessionOrganization("global", ""); err != nil {
		t.Fatal(err)
	}
	return app, ctrl, runtime, prov, path
}

func TestAIRenameCanonicalSessionPreservesManualRenameAndSurvivesTabClose(t *testing.T) {
	for _, change := range []string{"manual-title", "manual-topic", "binding"} {
		t.Run(change, func(t *testing.T) {
			app, ctrl, runtime, prov, _ := newCanonicalTitleFixture(t)
			appendSessionTestMessage(t, runtime, "user", provider.Message{ID: "user", Role: provider.RoleUser, Content: "rename this session"})
			prov.started = make(chan struct{})
			prov.chunks = make(chan provider.Chunk, 2)
			result := make(chan error, 1)
			go func() {
				_, err := app.AIRenameSessionTarget(SessionSelector{TopicID: "topic-canonical"})
				result <- err
			}()
			select {
			case <-prov.started:
			case err := <-result:
				t.Fatalf("rename stopped before provider: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("provider did not start")
			}
			switch change {
			case "manual-title":
				if err := app.RenameSession(sessionRoute(runtime.Ref().SessionID), "manual title"); err != nil {
					t.Fatal(err)
				}
			case "manual-topic":
				if err := app.RenameTopic("topic-canonical", "manual topic title"); err != nil {
					t.Fatal(err)
				}
			default:
				app.mu.Lock()
				app.tabs["test"].Ctrl = nil
				app.mu.Unlock()
			}
			prov.chunks <- provider.Chunk{Type: provider.ChunkText, Text: "stale AI title"}
			prov.chunks <- provider.Chunk{Type: provider.ChunkDone}
			close(prov.chunks)
			resultErr := <-result
			if change == "binding" {
				if resultErr != nil {
					t.Fatalf("tab close cancelled persistent rename: %v", resultErr)
				}
			} else if resultErr == nil {
				t.Fatal("manual title change did not reject stale AI completion")
			}
			info, err := ctrl.SessionService().Query().Stat(t.Context(), runtime.Ref())
			if err != nil ||
				(change != "binding" && info.Title == "stale AI title") ||
				(change == "binding" && info.Title != "stale AI title") ||
				(change == "manual-title" && info.Title != "manual title") {
				t.Fatalf("title = %+v, %v", info, err)
			}
			if change == "manual-topic" && info.Title != "manual topic title" {
				t.Fatal("manual sidebar title was overwritten")
			}
		})
	}
}

func newDesktopSessionTitleController(dir, path string, prov provider.Provider) *control.Controller {
	return control.New(control.Options{
		SessionDir:  dir,
		SessionPath: path,
		ModelRef:    "test/title-model",
		ProviderResolver: &provider.StaticResolver{
			Descriptors: []provider.Descriptor{{Ref: "test/title-model"}},
			Providers:   map[string]provider.Provider{"test/title-model": prov},
		},
	})
}

func installDesktopSessionTitleTab(app *App, ctrl *control.Controller, topicID, path string) {
	app.setTestCtrl(ctrl, "test/title-model")
	app.mu.Lock()
	tab := app.tabs["test"]
	tab.TopicID = topicID
	tab.SessionPath = path
	app.mu.Unlock()
}

func TestDelayedTitleCallbackProjectsCurrentCanonicalTitle(t *testing.T) {
	app, tab, _, ref := canonicalTitleFixture(t)
	service := app.desktopSessionService("")
	for _, title := range []string{"AI title", "Newer manual title"} {
		if err := service.SetTitle(t.Context(), ref, title); err != nil {
			t.Fatal(err)
		}
	}
	for _, callbackTitle := range []string{"Newer manual title", "AI title"} {
		if err := app.onSessionTitleChanged("", ref.SessionID, callbackTitle); err != nil {
			t.Fatal(err)
		}
	}
	if tab.TopicTitle != "Newer manual title" {
		t.Fatalf("stale callback title: %q", tab.TopicTitle)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil || state.Presentation[ref.SessionID].Title != "Newer manual title" {
		t.Fatalf("projection: %+v %v", state.Presentation[ref.SessionID], err)
	}
}

func TestIndependentSessionTitleOverridesSharedTopicTitle(t *testing.T) {
	app, root, refs := canonicalOrganizationFixtureWithTopic(t, "shared", "one", "two")
	if err := app.RenameSession(sessionRoute(refs["one"].SessionID), "Independent title"); err != nil {
		t.Fatal(err)
	}
	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("current page=%+v %v", page, err)
	}
	for _, row := range page.Items {
		if row.Session.SessionID == "one" && row.Label != "Independent title" {
			t.Fatal("session title did not override the shared topic")
		}
		if row.Session.SessionID == "two" && row.Label == "Independent title" {
			t.Fatal("session title leaked to its sibling")
		}
	}
}

func TestSessionTitleTranscriptIsBounded(t *testing.T) {
	transcript := sessionTitleTranscript([]string{
		strings.Repeat("a", aiSessionTitleMaxTurnRunes+10), "second", "third", "ignored",
	})
	parts := strings.Split(transcript, "\n\n")
	if len(parts) != aiSessionTitleMaxTurns || len([]rune(parts[0])) != aiSessionTitleMaxTurnRunes {
		t.Fatalf("parts = %d first runes = %d", len(parts), len([]rune(parts[0])))
	}
}
