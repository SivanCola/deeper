package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
)

func TestHistoryColdQueryRejectsNonlocalSessionIdentity(t *testing.T) {
	srv, ctrl, _, current := newExclusiveSessionServe(t)
	for _, id := range []string{"../escape", "/outside", "session-id:../escape", "session-id:/outside"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/history?sessionId="+url.QueryEscape(id), nil)
		query, ref, ok := srv.canonicalSessionQuery(recorder, request)
		if ok || query != nil || ref != (session.SessionRef{}) || recorder.Code != http.StatusConflict {
			t.Fatalf("nonlocal identity %q was admitted: ref=%+v status=%d", id, ref, recorder.Code)
		}
	}
	if ref, ok := ctrl.SessionRef(); !ok || ref != current {
		t.Fatal("rejected history query changed the active session")
	}
}

func TestDeleteSessionDeletesCanonicalIdentity(t *testing.T) {
	legacyDir := t.TempDir()
	v4Root := filepath.Join(t.TempDir(), "sessions-v4")
	service, err := session.NewService("serve-test", session.NewFilesystemPersistence(v4Root))
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: legacyDir, SessionService: service, ExclusiveSession: true})
	current, err := ctrl.BindFreshSession(t.Context(), "current")
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "canonical-target"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), target.Ref()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ctrl.Close)
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	srv := httptest.NewServer(operatorHandler(newLifecycleTestServer(t, ctrl, NewBroadcaster(), config.ServeConfig{})))
	defer srv.Close()
	post := func(body string) int {
		resp, err := http.Post(srv.URL+"/delete-session", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := post(`{"name":"` + current.SessionID + `","sessionId":"` + current.SessionID + `"}`); got != http.StatusConflict {
		t.Fatalf("active canonical delete status = %d, want 409", got)
	}
	if got := post(`{"name":"canonical-target","sessionId":"canonical-target"}`); got != http.StatusNoContent {
		t.Fatalf("canonical delete status = %d, want 204", got)
	}
	if _, err := os.Stat(filepath.Join(v4Root, "canonical-target")); !os.IsNotExist(err) {
		t.Fatalf("canonical session still exists or stat failed unexpectedly: %v", err)
	}
}

func newExclusiveSessionServe(t *testing.T) (*Server, *control.Controller, *session.Service, session.SessionRef) {
	t.Helper()
	return newExclusiveSessionServeWithOptions(t, nil)
}

func TestExtensionReloadRetainsCurrentRuntimeAndFrameIdentity(t *testing.T) {
	srv, old, service, ref := newExclusiveSessionServe(t)
	defer srv.Close()
	_, runtime, _ := old.SessionBinding()
	if err := old.SetInboxPausedPassive(true); err != nil {
		t.Fatal(err)
	}
	if _, err := old.EnqueueInbox(control.InboxRequest{Submit: "saved before reload"}); err != nil {
		t.Fatal(err)
	}
	srv.rebuildControllerWithOptions = func(_ context.Context, current *control.Controller, _ string, opts boot.Options) (*control.Controller, error) {
		if current != old || opts.SessionService != service || opts.SessionRuntime != runtime {
			t.Fatal("reload selected another session binding")
		}
		return serveModelReplacement(opts, nil), nil
	}
	if err := srv.reloadExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	next := srv.ctl().(*control.Controller)
	if next == old {
		t.Fatal("reload did not replace controller")
	}
	_, retained, bound := next.SessionBinding()
	if !bound || retained != runtime {
		t.Fatal("reload replaced the current runtime")
	}
	if queue := next.InboxSnapshot(); !queue.Paused || len(queue.Items) != 1 {
		t.Fatalf("reload lost the pending queue: %+v", queue)
	}
	frames, stop := srv.bc.Subscribe()
	defer stop()
	srv.tagFor(next).Emit(event.Event{Kind: event.Notice, Text: "after reload"})
	frame := nextServeProtocolFrame(t, frames, nil)
	if frame.Kind != "notice" || frame.SessionID != ref.SessionID || !frame.SessionCurrent {
		t.Fatalf("reload lost frame identity: %+v", frame)
	}
}

func TestExtensionReloadRejectsCandidateWithoutCurrentRuntime(t *testing.T) {
	srv, old, _, ref := newExclusiveSessionServe(t)
	defer srv.Close()
	var candidate *control.Controller
	srv.rebuildControllerWithOptions = func(_ context.Context, _ *control.Controller, _ string, opts boot.Options) (*control.Controller, error) {
		opts.SessionRuntime = nil
		candidate = serveModelReplacement(opts, nil)
		return candidate, nil
	}
	if err := srv.reloadExtensions(t.Context()); err == nil {
		t.Fatal("reload accepted a candidate with no current runtime")
	}
	if srv.ctl() != old {
		t.Fatal("failed reload changed the current owner")
	}
	if got, bound := old.SessionRef(); !bound || got != ref {
		t.Fatal("failed reload lost the original identity")
	}
	select {
	case <-candidate.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("rejected candidate leaked its resources")
	}
	if err := old.Snapshot(); err != nil {
		t.Fatalf("failed reload retired the original writer: %v", err)
	}
}

func TestExclusiveSessionPermissionPresetDoesNotFollowTheForegroundController(t *testing.T) {
	restoreSandbox := control.SetPresetSandboxForTest(true)
	t.Cleanup(restoreSandbox)
	srv, ctrl, service, first := newExclusiveSessionServe(t)
	httpServer := httptest.NewServer(operatorHandler(srv))
	defer httpServer.Close()
	target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), target.Ref()); err != nil {
		t.Fatal(err)
	}
	set := func(preset string) {
		t.Helper()
		resp, err := http.Post(httpServer.URL+"/composer-profile", "application/json", strings.NewReader(`{"collaborationMode":"normal","toolApprovalMode":"`+preset+`","goal":""}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("set %s: %d", preset, resp.StatusCode)
		}
	}
	open := func(id, want string) {
		t.Helper()
		resp, err := http.Post(httpServer.URL+"/resume", "application/json", strings.NewReader(`{"hostId":"serve-test","sessionId":"`+id+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("resume %s: %d", id, resp.StatusCode)
		}
		if got := ctrl.PermissionSnapshot(); got.SessionID != id || got.Preset != want {
			t.Fatalf("resume %s: snapshot = %+v, want preset %s", id, got, want)
		}
	}
	set(control.ToolApprovalDangerFullAccess)
	open("target", control.ToolApprovalWorkspaceWrite)
	set(control.ToolApprovalReadOnly)
	open(first.SessionID, control.ToolApprovalDangerFullAccess)
	open("target", control.ToolApprovalReadOnly)
}

func TestExclusiveSessionPermissionPresetRoutesRememberTheSelectedSession(t *testing.T) {
	restoreSandbox := control.SetPresetSandboxForTest(true)
	t.Cleanup(restoreSandbox)
	routes := []struct {
		name   string
		path   string
		body   string
		status int
		serve  func(*Server, http.ResponseWriter, *http.Request)
	}{
		{"preset", "/permission/preset", `{"preset":"workspace-write","expectedRevision":2}`, http.StatusOK, func(s *Server, w http.ResponseWriter, r *http.Request) {
			s.foregroundMutation(s.permissionPreset)(w, r)
		}},
		{"explicit mode", "/tool-approval-mode", `{"mode":"workspace-write"}`, http.StatusNoContent, func(s *Server, w http.ResponseWriter, r *http.Request) {
			s.foregroundMutation(s.toolApprovalMode)(w, r)
		}},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			srv, ctrl, service, first := newExclusiveSessionServe(t)
			target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "target"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := target.Session().Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := service.Close(t.Context(), target.Ref()); err != nil {
				t.Fatal(err)
			}
			set := httptest.NewRecorder()
			srv.composerProfile(set, httptest.NewRequest(http.MethodPost, "/composer-profile", strings.NewReader(`{"collaborationMode":"normal","toolApprovalMode":"danger-full-access","goal":""}`)))
			if set.Code != http.StatusOK {
				t.Fatalf("set first: %d %s", set.Code, set.Body.String())
			}
			resume := func(id string) {
				t.Helper()
				w := httptest.NewRecorder()
				srv.resume(w, httptest.NewRequest(http.MethodPost, "/resume", strings.NewReader(`{"hostId":"serve-test","sessionId":"`+id+`"}`)))
				if w.Code != http.StatusNoContent {
					t.Fatalf("resume %s: %d %s", id, w.Code, w.Body.String())
				}
			}
			resume("target")
			before := ctrl.PermissionSnapshot()
			body := route.body
			if route.name == "preset" {
				body = `{"preset":"workspace-write","expectedRevision":` + fmt.Sprint(ctrl.PermissionSnapshot().Revision) + `}`
			}
			req := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(body))
			req.Header.Set(expectedSessionIDHeader, "target")
			w := httptest.NewRecorder()
			route.serve(srv, w, req)
			if w.Code != route.status {
				t.Fatalf("set target: %d %s", w.Code, w.Body.String())
			}
			if route.name == "preset" && ctrl.PermissionSnapshot().Revision <= before.Revision {
				t.Fatal("an explicit same-value preset choice did not invalidate the prior revision")
			}
			resume(first.SessionID)
			if got := ctrl.PermissionSnapshot().Preset; got != control.ToolApprovalDangerFullAccess {
				t.Fatalf("first preset = %q", got)
			}
			if route.name == "preset" {
				stale := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(`{"preset":"danger-full-access","expectedRevision":`+fmt.Sprint(ctrl.PermissionSnapshot().Revision)+`}`))
				stale.Header.Set(expectedSessionIDHeader, "target")
				refused := httptest.NewRecorder()
				route.serve(srv, refused, stale)
				if refused.Code != http.StatusConflict {
					t.Fatalf("stale target update status = %d, want conflict", refused.Code)
				}
				if got := ctrl.PermissionSnapshot().Preset; got != control.ToolApprovalDangerFullAccess {
					t.Fatalf("stale target update changed first preset to %q", got)
				}
			}
			resume("target")
			if got := ctrl.PermissionSnapshot().Preset; got != control.ToolApprovalWorkspaceWrite {
				t.Fatalf("target preset = %q", got)
			}
		})
	}
}

// newExclusiveSessionServeWithOptions lets a test shape the foreground
// controller (for example install a blocking Runner so a turn can be held open)
// before the exclusive identity is bound.
func newExclusiveSessionServeWithOptions(t *testing.T, configure func(*control.Options)) (*Server, *control.Controller, *session.Service, session.SessionRef) {
	t.Helper()
	bc := NewBroadcaster()
	tag := newSessionTagSink(bc)
	root := filepath.Join(t.TempDir(), "sessions-v4")
	service, err := session.NewService("serve-test", session.NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown session service: %v", err)
		}
	})
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	opts := control.Options{
		Executor: exec, Sink: tag, SessionDir: t.TempDir(), SessionService: service, ExclusiveSession: true,
	}
	if configure != nil {
		configure(&opts)
	}
	ctrl := control.New(opts)
	ref, err := ctrl.BindFreshSession(t.Context(), "current")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ctrl.Close)
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	srv := New(ctrl, bc, config.ServeConfig{})
	// Production serve hosts register a frame tag per controller; the takeover
	// tests assert its identity, so wire the sink the CLI server would.
	if id, bound := ctrl.SessionRef(); bound {
		tag.SetIdentity(id.SessionID)
	}
	srv.RegisterSessionTag(ctrl, tag)
	return srv, ctrl, service, ref
}

func TestExclusiveV3SessionsAndResumeUseImmutableIdentity(t *testing.T) {
	srv, ctrl, service, current := newExclusiveSessionServe(t)
	target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), target.Ref()); err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRecorder()
	srv.sessions(list, httptest.NewRequest(http.MethodGet, "/sessions", nil))
	var rows []sessionListEntry
	if err := json.Unmarshal(list.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	foundCurrent, foundTarget := false, false
	for _, row := range rows {
		if row.SessionID == current.SessionID && row.HostID == current.HostID && row.Current && row.Path == "" {
			foundCurrent = true
		}
		if row.SessionID == "target" && row.HostID == current.HostID && row.Path == "" {
			foundTarget = true
		}
	}
	if !foundCurrent || !foundTarget {
		t.Fatalf("v3 rows = %+v", rows)
	}

	resume := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/resume", strings.NewReader(`{"hostId":"serve-test","sessionId":"target"}`))
	srv.resume(resume, req)
	if resume.Code != http.StatusNoContent {
		t.Fatalf("resume status = %d: %s", resume.Code, resume.Body.String())
	}
	if got := resume.Header().Get(sessionIDHeader); got != "target" {
		t.Fatalf("resume session id = %q", got)
	}
	if got, ok := ctrl.SessionRef(); !ok || got.SessionID != "target" {
		t.Fatalf("controller identity = %+v, bound=%v path=%q", got, ok, ctrl.SessionRoute())
	}
}

func TestSessionsReportsFinalizingExclusiveRuntimeAsRunning(t *testing.T) {
	srv, ctrl, service, ref := newExclusiveSessionServe(t)
	runtime, ok := service.Runtime(ref)
	if !ok {
		t.Fatal("current runtime is not published")
	}
	generation := ctrl.ExecutionGeneration()
	runtime.NoteExecution(generation, session.RuntimeFinalizing, "terminal_commit")
	defer runtime.NoteExecution(generation, session.RuntimeIdle, "")

	list := httptest.NewRecorder()
	srv.sessions(list, httptest.NewRequest(http.MethodGet, "/sessions", nil))
	var rows []sessionListEntry
	if err := json.Unmarshal(list.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.SessionID == ref.SessionID {
			if !row.Running {
				t.Fatalf("finalizing session row = %+v, want running", row)
			}
			return
		}
	}
	t.Fatalf("session %q missing from rows %+v", ref.SessionID, rows)
}

func TestExclusiveV3MissingResumeDoesNotCreateOrReplaceCurrent(t *testing.T) {
	srv, ctrl, service, current := newExclusiveSessionServe(t)
	resume := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/resume", strings.NewReader(`{"hostId":"serve-test","sessionId":"missing"}`))
	srv.resume(resume, req)
	if resume.Code != http.StatusConflict {
		t.Fatalf("resume status = %d, want 409", resume.Code)
	}
	if got, ok := ctrl.SessionRef(); !ok || got != current {
		t.Fatalf("current identity changed to %+v, bound=%v", got, ok)
	}
	if _, err := service.Query().Snapshot(t.Context(), session.SessionRef{HostID: "serve-test", SessionID: "missing"}); err == nil {
		t.Fatal("missing Open created a session")
	}
}

func TestExclusiveV3RotationAndMutationFenceReturnSessionID(t *testing.T) {
	srv, ctrl, _, current := newExclusiveSessionServe(t)
	stale := httptest.NewRequest(http.MethodPost, "/cancel", nil)
	stale.Header.Set(expectedSessionIDHeader, "stale")
	if err := srv.expectedSessionErrorLocked(stale); err == nil {
		t.Fatal("stale immutable identity passed mutation fence")
	}
	matching := httptest.NewRequest(http.MethodPost, "/cancel", nil)
	matching.Header.Set(expectedSessionIDHeader, current.SessionID)
	if err := srv.expectedSessionErrorLocked(matching); err != nil {
		t.Fatalf("matching identity rejected: %v", err)
	}

	rotate := httptest.NewRecorder()
	srv.newSession(rotate, httptest.NewRequest(http.MethodPost, "/new", nil))
	if rotate.Code != http.StatusNoContent {
		t.Fatalf("new status = %d: %s", rotate.Code, rotate.Body.String())
	}
	ref, ok := ctrl.SessionRef()
	if !ok || ref.SessionID == current.SessionID || ref.SessionID == "" {
		t.Fatalf("rotated identity = %+v, bound=%v", ref, ok)
	}
	if got := rotate.Header().Get(sessionIDHeader); got != ref.SessionID {
		t.Fatalf("new response session id = %q, want %q", got, ref.SessionID)
	}
	if got := rotate.Header().Get(sessionPathHeader); got != remoteSessionIDQueryPrefix+ref.SessionID {
		t.Fatalf("rotation route = %q, want current identity %q", got, ref.SessionID)
	}
}

// The persistence list caps one page at 100 rows ordered by the random
// session id, so a workspace with more canonical sessions hid an arbitrary
// subset — including a session another runtime had just taken over. The
// handler must follow NextCursor and surface every row in one response.
func TestSessionsListsBeyondFirstHundredCanonicalSessions(t *testing.T) {
	v4Root := filepath.Join(robustTempDir(t), "sessions-v4")
	service, err := session.NewService("serve-test", session.NewFilesystemPersistence(v4Root))
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: robustTempDir(t), SessionService: service, ExclusiveSession: true})
	const total = 103
	for i := range total {
		created, err := service.Create(t.Context(), session.CreateOptions{SessionID: fmt.Sprintf("s%03d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Close(t.Context(), created.Ref()); err != nil {
			t.Fatal(err)
		}
	}
	// A catalog full of pending metadata keeps the query's background rebuild
	// queue busy, which races the lifecycle fixture's writer-retire check.
	// Drive the queue to quiescence before the handler under test runs.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		page, err := service.Query().List(t.Context(), "", 100)
		if err != nil {
			t.Fatal(err)
		}
		pending := 0
		for _, info := range page.Sessions {
			if info.MetadataStatus != session.MetadataReady {
				pending++
			}
		}
		if pending == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Cleanup(ctrl.Close)
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	// Service-backed controllers park their runtime in the idle cache after
	// close, so the lifecycle fixture's writer-retire wait does not apply;
	// these listing tests only need the HTTP surface.
	srv := New(ctrl, NewBroadcaster(), config.ServeConfig{})
	t.Cleanup(srv.Close)
	recorder := httptest.NewRecorder()
	srv.sessions(recorder, httptest.NewRequest(http.MethodGet, "/sessions", nil))
	var rows []sessionListEntry
	if err := json.Unmarshal(recorder.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.SessionID] = true
	}
	for i := range total {
		if !seen[fmt.Sprintf("s%03d", i)] {
			t.Fatalf("session s%03d missing from the list (%d rows returned)", i, len(rows))
		}
	}
}

// migratedDeleteFixture hosts an exclusive foreground beside a legacy session
// dir and a v4 root so tests can stage migration maps and drive
// /delete-session against canonical rows.
