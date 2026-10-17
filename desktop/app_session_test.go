package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/jobs"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func TestClearActiveSessionRuntimeSupersedesInFlightStartupBuild(t *testing.T) {
	app, tab, ctrl, _ := currentClearFixture(t, nil, nil)
	before, _ := ctrl.SessionRef()
	buildCtx, buildCancel := context.WithCancel(context.Background())
	tab.buildGeneration, tab.buildCancel = 1, buildCancel
	if _, err := app.clearActiveSessionRuntime(tab, ctrl); err != nil {
		t.Fatal(err)
	}
	after, bound := ctrl.SessionRef()
	if !bound || before == after || tab.SessionID != after.SessionID {
		t.Fatalf("clear did not rotate the current identity: before=%+v after=%+v tab=%q", before, after, tab.SessionID)
	}
	if tab.Ctrl != ctrl {
		t.Fatal("clear replaced the current controller instead of rotating its session")
	}
	assertTabBuildSuperseded(t, app, tab, 1, buildCtx)
}

func TestClearActiveSessionRuntimeRefusesReplacedTabWithoutTouchingItsSession(t *testing.T) {
	app, tab, ctrl, _ := currentClearFixture(t, nil, nil)
	before, _ := ctrl.SessionRef()
	replacement := &WorkspaceTab{ID: tab.ID, Scope: "global"}
	app.tabs[tab.ID] = replacement
	_, err := app.clearActiveSessionRuntime(tab, ctrl)
	if err == nil || !strings.Contains(err.Error(), "changed while clearing") {
		t.Fatalf("clear error = %v, want tab-changed error", err)
	}
	after, bound := ctrl.SessionRef()
	if !bound || after != before || tab.Ctrl != ctrl || replacement.Ctrl != nil {
		t.Fatal("stale clear changed a runtime binding")
	}
	if _, err := ctrl.SessionService().Query().History(t.Context(), before); err != nil {
		t.Fatalf("stale clear destroyed the prior session: %v", err)
	}
}

func TestClearSessionCancelsRunningRuntimeAndKeepsTopic(t *testing.T) {
	prov := &blockingPinnedProvider{started: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{})}
	app, tab, ctrl, _ := currentClearFixture(t, prov, nil)
	before, _ := ctrl.SessionRef()
	tab.TopicID, tab.TopicTitle = "topic_clear", "Clear topic"
	go ctrl.Submit("work")
	select {
	case <-prov.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	go func() { <-prov.cancelled; close(prov.release) }()
	if _, err := app.ClearSession(); err != nil {
		t.Fatal(err)
	}
	waitNotRunning(t, ctrl)
	after, bound := ctrl.SessionRef()
	if !bound || after == before || tab.SessionID != after.SessionID {
		t.Fatalf("clear did not rotate identity: %+v -> %+v", before, after)
	}
	if tab.TopicID != "topic_clear" || tab.TopicTitle != "Clear topic" {
		t.Fatalf("clear changed topic: %+v", tab)
	}
	if messagesHaveConversationContent(ctrl.History()) {
		t.Fatal("cleared session retained conversation content")
	}
}

func TestClearSessionRemovesRunningJobArtifacts(t *testing.T) {
	jm := jobs.NewManager(event.Discard)
	app, _, ctrl, _ := currentClearFixture(t, nil, jm)
	ref, _ := ctrl.SessionRef()
	statePath, err := ctrl.SessionService().SessionDir(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}), make(chan struct{})
	jm.StartForSession(ref.SessionID, "bash", "clear artifact", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		defer close(stopped)
		return "", ctx.Err()
	})
	<-started
	jobsDir := jobs.ArtifactDir(statePath)
	if _, err := os.Stat(jobsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ClearSession(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("clear returned before the captured session's job stopped")
	}
	if _, err := os.Stat(jobsDir); !os.IsNotExist(err) {
		t.Fatalf("cleared job artifacts remain: %v", err)
	}
	if len(ctrl.Jobs()) != 0 {
		t.Fatal("fresh session inherited old background jobs")
	}
}

func TestClearSessionWaitsForUncooperativeJobBeforeRotatingIdentity(t *testing.T) {
	manager := jobs.NewManager(event.Discard, jobs.WithTeardownGrace(10*time.Millisecond))
	app, _, ctrl, _ := currentClearFixture(t, nil, manager)
	before, _ := ctrl.SessionRef()
	started, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseJob := func() { once.Do(func() { close(release) }) }
	defer releaseJob()
	manager.StartForSession(before.SessionID, "bash", "blocked cleanup", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		<-release
		defer close(stopped)
		return "", ctx.Err()
	})
	<-started
	if _, err := app.ClearSession(); err == nil || !strings.Contains(err.Error(), "session jobs") {
		t.Fatalf("clear error = %v, want unfinished-job error", err)
	}
	if after, ok := ctrl.SessionRef(); !ok || after != before {
		t.Fatal("clear rotated while old work could still write")
	}
	if !manager.IsDestroying(before.SessionID) {
		t.Fatal("clear released the job admission fence before cleanup stopped")
	}
	releaseJob()
	<-stopped
	deadline := time.Now().Add(2 * time.Second)
	for manager.IsDestroying(before.SessionID) {
		if time.Now().After(deadline) {
			t.Fatal("finished job did not release its destroy fence")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := app.ClearSession(); err != nil {
		t.Fatalf("clear retry after job exit: %v", err)
	}
	if after, ok := ctrl.SessionRef(); !ok || after == before {
		t.Fatal("clear retry did not rotate the identity")
	}
}

func currentClearFixture(t *testing.T, prov provider.Provider, manager *jobs.Manager) (*App, *WorkspaceTab, *control.Controller, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := newCurrentRebuildController(t, app, control.Options{Runner: exec, Executor: exec, Jobs: manager, Sink: event.Discard})
	app.setTestCtrl(ctrl, "")
	tab := app.activeTab()
	setCurrentRebuildTabIdentity(t, app, tab, ctrl)
	path, err := ctrl.PinnedContextPath(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return app, tab, ctrl, path
}

func TestSearchFileRefsFindsNestedBasename(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := robustTempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "frontend", "wailsjs", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", "wailsjs", "runtime", "runtime.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", "Thumbs.db"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", ".DS_Store"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "runtime.js"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, noise := range []string{".codex", ".npm", ".pnpm-store", "bin", "dist", "stage", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, noise), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, noise, "runtime.js"), []byte("noise"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "desktop", "frontend", "wailsjs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "desktop", "frontend", "wailsjs", "runtime.js"), []byte("generated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "product", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "product", "bin", "runtime.js"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	listed := app.ListDir("")
	for name, shown := range map[string]bool{".codex": false, ".npm": false, ".pnpm-store": false, "dist": false, "bin": true, "stage": true, "tmp": true} {
		if hasDirEntry(listed, name) != shown {
			t.Fatalf("ListDir shows %q = %v, want %v (only @-search skips generic names); got %+v", name, !shown, shown, listed)
		}
	}
	desktopFrontend := app.ListDir("desktop/frontend")
	if !hasDirEntry(desktopFrontend, "wailsjs") {
		t.Fatalf("ListDir should show desktop/frontend/wailsjs, got %+v", desktopFrontend)
	}
	frontendEntries := app.ListDir("frontend")
	for _, hidden := range []string{".DS_Store", "Thumbs.db"} {
		if hasDirEntry(frontendEntries, hidden) {
			t.Fatalf("ListDir should hide local noise file %q, got %+v", hidden, frontendEntries)
		}
	}

	got := app.SearchFileRefs("runtime.js")
	if !hasDirEntry(got, "frontend/wailsjs/runtime/runtime.js") {
		t.Fatalf("SearchFileRefs(runtime.js) should find nested workspace file, got %+v", got)
	}
	if !hasDirEntry(got, "product/bin/runtime.js") {
		t.Fatalf("SearchFileRefs should keep non-root bin directories searchable, got %+v", got)
	}
	if hasDirEntry(got, "node_modules/pkg/runtime.js") {
		t.Fatalf("SearchFileRefs should skip node_modules noise, got %+v", got)
	}
	for _, hidden := range []string{
		".codex/runtime.js",
		".npm/runtime.js",
		".pnpm-store/runtime.js",
		"bin/runtime.js",
		"desktop/frontend/wailsjs/runtime.js",
		"dist/runtime.js",
		"stage/runtime.js",
		"tmp/runtime.js",
	} {
		if hasDirEntry(got, hidden) {
			t.Fatalf("SearchFileRefs should skip local noise %q, got %+v", hidden, got)
		}
	}
	if noise := app.SearchFileRefs("Thumbs"); hasDirEntry(noise, "frontend/Thumbs.db") {
		t.Fatalf("SearchFileRefs should skip Thumbs.db noise, got %+v", noise)
	}
	if noise := app.SearchFileRefs(".DS"); hasDirEntry(noise, "frontend/.DS_Store") {
		t.Fatalf("SearchFileRefs should skip .DS_Store noise even for dot-prefixed search, got %+v", noise)
	}
}

func TestFileRefsUseActiveTabWorkspaceRoot(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	launchRoot := robustTempDir(t)
	projectRoot := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(launchRoot, "launch-only.txt"), []byte("wrong"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, "frontend", "wailsjs", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectFile := filepath.Join(projectRoot, "frontend", "wailsjs", "runtime", "runtime.js")
	if err := os.WriteFile(projectFile, []byte("right workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(launchRoot); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	tab := &WorkspaceTab{ID: "project", Scope: "project", WorkspaceRoot: projectRoot}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.activeTabID = tab.ID

	listed := app.ListDir("")
	if !hasDirEntry(listed, "frontend") {
		t.Fatalf("ListDir should list active project root, got %+v", listed)
	}
	if hasDirEntry(listed, "launch-only.txt") {
		t.Fatalf("ListDir leaked launch cwd entries, got %+v", listed)
	}

	found := app.SearchFileRefs("runtime.js")
	if !hasDirEntry(found, "frontend/wailsjs/runtime/runtime.js") {
		t.Fatalf("SearchFileRefs should search active project root, got %+v", found)
	}
	preview := app.ReadFile("frontend/wailsjs/runtime/runtime.js")
	if preview.Err != "" || preview.Body != "right workspace" {
		t.Fatalf("ReadFile active project preview = %+v, want project file", preview)
	}
}

func TestFileRefsForTabIgnoreActiveParentWorkspace(t *testing.T) {
	parentRoot := robustTempDir(t)
	childRoot := filepath.Join(parentRoot, "child")
	if err := os.MkdirAll(childRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentRoot, "parent-only.txt"), []byte("parent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentRoot, "shared.txt"), []byte("parent shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childRoot, "child-only.txt"), []byte("child"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childRoot, "shared.txt"), []byte("child shared"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"parent": {ID: "parent", Scope: "project", WorkspaceRoot: parentRoot},
			"child":  {ID: "child", Scope: "project", WorkspaceRoot: childRoot},
		},
		activeTabID: "parent",
	}

	listed := app.ListDirForTab("child", "")
	if !hasDirEntry(listed, "child-only.txt") || hasDirEntry(listed, "parent-only.txt") {
		t.Fatalf("ListDirForTab(child) = %+v, want only child workspace entries", listed)
	}
	found := app.SearchFileRefsForTab("child", "child-only")
	if !hasDirEntry(found, "child-only.txt") {
		t.Fatalf("SearchFileRefsForTab(child) = %+v, want child-only.txt", found)
	}
	preview := app.ReadFileForTab("child", "shared.txt")
	if preview.Err != "" || preview.Body != "child shared" {
		t.Fatalf("ReadFileForTab(child) = %+v, want child workspace file", preview)
	}
	path, ok, err := app.workspaceOrExternalPathForTab("child", "shared.txt")
	if err != nil || !ok || path != filepath.Join(childRoot, "shared.txt") {
		t.Fatalf("workspaceOrExternalPathForTab(child) = (%q, %v, %v)", path, ok, err)
	}

	legacy := app.ReadFile("shared.txt")
	if legacy.Err != "" || legacy.Body != "parent shared" {
		t.Fatalf("ReadFile legacy active-tab behavior = %+v, want parent workspace file", legacy)
	}
}

func TestFileRefsIncludeRegisteredExternalFolderChildren(t *testing.T) {
	workspace := robustTempDir(t)
	external := filepath.Join(robustTempDir(t), "Folder With Spaces")
	if err := os.MkdirAll(filepath.Join(external, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "src", "outside.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectedExternal := external
	if resolved, err := filepath.EvalSymlinks(external); err == nil {
		expectedExternal = resolved
	}
	expectedDisplayPath := filepath.ToSlash(expectedExternal)

	ctrl := &control.Controller{}
	token, _, err := ctrl.RegisterExternalFolderRef(external)
	if err != nil {
		t.Fatalf("RegisterExternalFolderRef: %v", err)
	}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"project": {ID: "project", WorkspaceRoot: workspace, Ctrl: ctrl},
			"other":   {ID: "other", WorkspaceRoot: robustTempDir(t)},
		},
		activeTabID: "other",
	}

	listed := app.ListDirForTab("project", token+"/src/")
	if len(listed) != 1 ||
		listed[0].Name != "outside.txt" ||
		listed[0].Path != token+"/src/outside.txt" ||
		listed[0].DisplayPath != expectedDisplayPath+"/src/outside.txt" {
		t.Fatalf("ListDir external src = %+v, want outside token/display path", listed)
	}

	found := app.SearchFileRefsForTab("project", "outside")
	var externalHit *DirEntry
	for i := range found {
		if found[i].Path == token+"/src/outside.txt" {
			externalHit = &found[i]
			break
		}
	}
	if externalHit == nil || externalHit.DisplayName != "Folder With Spaces/src/outside.txt" || externalHit.DisplayPath != expectedDisplayPath+"/src/outside.txt" {
		t.Fatalf("SearchFileRefs external hit = %+v, all results %+v", externalHit, found)
	}

	preview := app.ReadFileForTab("project", token+"/src/outside.txt")
	if preview.Err != "" || preview.Body != "outside" {
		t.Fatalf("ReadFile external token preview = %+v, want outside file body", preview)
	}
}

func TestWaitDestroyHandlesWaitsConcurrently(t *testing.T) {
	started := make(chan int, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()

	handle := func(id int) control.SessionDestroyHandle {
		return control.SessionDestroyHandle{WaitFor: func(time.Duration) jobs.TeardownResult {
			started <- id
			<-release
			return jobs.TeardownResult{TimedOut: []jobs.TeardownJob{{ID: strconv.Itoa(id)}}}
		}}
	}
	done := make(chan bool, 1)
	go func() { done <- waitDestroyHandles([]control.SessionDestroyHandle{handle(1), handle(2)}) }()

	seen := map[int]bool{}
	for len(seen) < 2 {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("destroy waits did not start concurrently; started=%v", seen)
		}
	}
	releaseAll()
	select {
	case timedOut := <-done:
		if !timedOut {
			t.Fatal("waitDestroyHandles lost timed-out result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitDestroyHandles did not return after all waits completed")
	}
}

func TestResumeSessionRejectsPathOutsideControllerSessionDir(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	activePath := filepath.Join(dirA, "active.jsonl")
	outsidePath := filepath.Join(dirB, "outside.jsonl")
	for _, path := range []string{activePath, outsidePath} {
		if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{SessionDir: dirA, Label: "test"}), "")
	defer app.activeCtrl().Close()

	if _, err := app.ResumeTranscriptSessionForTab("test", outsidePath); err == nil {
		t.Fatal("transcript resume should reject a file path")
	}
	if _, err := app.PreviewSession(outsidePath); err == nil {
		t.Fatal("PreviewSession should reject a transcript outside the active session dir")
	}
}

func BenchmarkDesktopListSessionsScoped(b *testing.B) {
	dirA := filepath.Join(b.TempDir(), "workspace-a-sessions")
	dirB := filepath.Join(b.TempDir(), "workspace-b-sessions")
	for _, dir := range []string{dirA, dirB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatalf("mkdir %s: %v", dir, err)
		}
		for i := range 120 {
			path := filepath.Join(dir, fmt.Sprintf("session-%03d.jsonl", i))
			body := fmt.Sprintf(`{"role":"user","content":"session %03d"}`+"\n", i)
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				b.Fatalf("write session: %v", err)
			}
		}
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{SessionDir: dirA, Label: "test"}), "")
	defer app.activeCtrl().Close()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		sessions := app.ListSessions()
		if len(sessions) != 120 {
			b.Fatalf("ListSessions len = %d, want 120", len(sessions))
		}
	}
}

func startNonCooperativeSessionJob(t *testing.T, jm *jobs.Manager, sessionID string) func() {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	jm.StartForSession(sessionID, "bash", "stuck job", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		<-release
		return "", ctx.Err()
	})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background job never started")
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		close(release)
	}
}

func TestSessionActionsWithoutControllerReturnError(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}}
	if err := app.NewSession(); err == nil {
		t.Error("NewSession with no controller must surface an error, not silently no-op")
	}
	if _, err := app.ClearSession(); err == nil {
		t.Error("ClearSession with no controller must surface an error")
	}

	app = &App{
		tabs:        map[string]*WorkspaceTab{"t1": {ID: "t1", StartupErr: "boot exploded"}},
		activeTabID: "t1",
	}
	err := app.NewSession()
	if err == nil || !strings.Contains(err.Error(), "boot exploded") {
		t.Errorf("error should carry the tab's startup failure, got %v", err)
	}
}
