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
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/jobs"
)

func TestDescribeSessionBindingWorkspaceKeepsWindowsPathReadable(t *testing.T) {
	path := `C:\Users\Jane Doe\Deepzora`
	want := `project workspace "C:\Users\Jane Doe\Deepzora"`
	if got := describeSessionBindingWorkspace("project", path); got != want {
		t.Fatalf("describeSessionBindingWorkspace = %q, want %q", got, want)
	}
}

func TestClearActiveSessionRuntimeSupersedesInFlightStartupBuild(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{Models: []string{"old-model"}, Default: "old-model",
		Name:      "old",
		Kind:      "openai",
		BaseURL:   "https://example.invalid/v1",
		Model:     "old-model",
		APIKeyEnv: "OLD_MODEL_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "clear-runtime-in-flight.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}

	oldSession := agent.NewSession("old system prompt")
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: sessionPath, Label: "old", Sink: event.Discard})

	app := NewApp()
	// A runtime is attached while an older async build is still in flight
	// (e.g. attached via topic activation); destroying the session must
	// invalidate that build so it cannot resurrect the destroyed session.
	buildCtx, buildCancel := context.WithCancel(context.Background())
	tab := &WorkspaceTab{
		ID:              "tab_clear",
		Scope:           "global",
		SessionPath:     sessionPath,
		model:           "old/old-model",
		Ready:           true,
		Ctrl:            oldCtrl,
		buildGeneration: 1,
		buildCancel:     buildCancel,
		disabledMCP:     map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(tab.releaseSessionLease)

	if _, err := app.clearActiveSessionRuntime(tab, oldCtrl); err != nil {
		t.Fatalf("clearActiveSessionRuntime: %v", err)
	}
	if tab.Ctrl == nil || tab.Ctrl == oldCtrl {
		t.Fatalf("clear did not install a fresh controller (ctrl=%v)", tab.Ctrl)
	}
	defer tab.Ctrl.Close()
	assertTabBuildSuperseded(t, app, tab, 1, buildCtx)
}

func TestClearActiveSessionRuntimeReleasesResourcesWhenTabReplaced(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{Models: []string{"old-model"}, Default: "old-model",
		Name:      "old",
		Kind:      "openai",
		BaseURL:   "https://example.invalid/v1",
		Model:     "old-model",
		APIKeyEnv: "OLD_MODEL_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "clear-runtime-replaced-tab.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}

	oldSession := agent.NewSession("old system prompt")
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: sessionPath, Label: "old", Sink: event.Discard})

	app := NewApp()
	tab := &WorkspaceTab{
		ID:          "tab_replaced",
		Scope:       "global",
		SessionPath: sessionPath,
		model:       "old/old-model",
		Ready:       true,
		Ctrl:        oldCtrl,
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	// The tab entry now points at a replacement struct (the tab was closed and
	// reopened while the clear ran off-lock), so the swap must not apply.
	replacement := &WorkspaceTab{ID: tab.ID, Scope: "global"}
	app.tabs = map[string]*WorkspaceTab{tab.ID: replacement}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(tab.releaseSessionLease)

	_, err := app.clearActiveSessionRuntime(tab, oldCtrl)
	if err == nil || !strings.Contains(err.Error(), "changed while clearing") {
		t.Fatalf("clearActiveSessionRuntime error = %v, want tab-changed error", err)
	}
	if replacement.Ctrl != nil {
		t.Fatalf("replacement tab controller = %v, want untouched nil", replacement.Ctrl)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("replaced tab controller = %v, want left on the destroyed runtime", tab.Ctrl)
	}
	if key := tab.sessionLeaseRuntimeKey(); key != "" {
		t.Fatalf("replaced tab still holds a session lease for %q; the fresh lease leaked", key)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("old session artifacts were not destroyed (stat err=%v)", err)
	}
}

func TestClearSessionCancelsRunningRuntimeAndKeepsTopic(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "clear-running.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"old"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	oldCtrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: path, Label: "test"})
	app := NewApp()
	app.projectTreeChangedHook = func() {}
	app.setTestCtrl(oldCtrl, "deepseek-flash/deepseek-flash")
	app.tabs["test"].TopicID = "topic_clear"
	app.tabs["test"].TopicTitle = "Clear topic"
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	oldCtrl.Submit("work")
	<-runner.started
	if _, err := app.ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	waitNotRunning(t, oldCtrl)
	tab := app.activeTab()
	if tab == nil || tab.Ctrl == nil {
		t.Fatalf("active tab/controller missing after clear")
	}
	if tab.Ctrl == oldCtrl {
		t.Fatalf("clear should replace the active controller after cancelling old work")
	}
	if tab.TopicID != "topic_clear" || tab.TopicTitle != "Clear topic" {
		t.Fatalf("clear changed topic identity: %+v", tab)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old cleared session artifacts should be removed, stat err = %v", err)
	}
	if got := tab.currentSessionPath(); got == "" || got == path {
		t.Fatalf("new session path = %q, want fresh path", got)
	}
}

func TestClearSessionRemovesRunningJobArtifacts(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "clear-running-job.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"old"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	jm := jobs.NewManager(event.Discard)
	oldCtrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	app := NewApp()
	app.projectTreeChangedHook = func() {}
	app.setTestCtrl(oldCtrl, "deepseek-flash/deepseek-flash")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	started := make(chan struct{})
	jm.StartForSession(agent.BranchID(path), "bash", "clear artifact", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	<-started
	jobsDir := jobs.ArtifactDir(path)
	if _, err := os.Stat(jobsDir); err != nil {
		t.Fatalf("job sidecar should exist before clear: %v", err)
	}

	if _, err := app.ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	if _, err := os.Stat(jobsDir); !os.IsNotExist(err) {
		t.Fatalf("old job sidecar should be removed after clear, stat err = %v", err)
	}
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
		return control.SessionDestroyHandle{Wait: func() jobs.TeardownResult {
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
	app.setTestCtrl(control.New(control.Options{SessionDir: dirA, SessionPath: activePath, Label: "test"}), "")
	defer app.activeCtrl().Close()

	if _, err := app.ResumeSession(outsidePath); err == nil {
		t.Fatal("ResumeSession should reject a transcript outside the active session dir")
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
	app.setTestCtrl(control.New(control.Options{SessionDir: dirA, SessionPath: filepath.Join(dirA, "session-000.jsonl"), Label: "test"}), "")
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

func startNonCooperativeSessionJob(t *testing.T, jm *jobs.Manager, sessionPath string) func() {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	jm.StartForSession(agent.BranchID(sessionPath), "bash", "stuck job", func(ctx context.Context, _ io.Writer) (string, error) {
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
