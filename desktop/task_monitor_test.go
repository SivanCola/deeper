package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/session"
	"deepzora/internal/taskcatalog"
	"deepzora/internal/taskmonitor"
)

func TestTaskMonitorColdTabUsesCurrentIdentityAndOwnWorkspace(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, other := t.TempDir(), t.TempDir()
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"cold":   {ID: "cold", Scope: "project", WorkspaceRoot: root, SessionID: "cold-current"},
		"active": {ID: "active", Scope: "project", WorkspaceRoot: other, SessionID: "other-current"},
	}
	app.activeTabID = "active"
	target, err := app.taskMonitorTargetForTab("cold")
	if err != nil {
		t.Fatal(err)
	}
	if target.sessionID != "cold-current" || target.sessionPath != sessionRoute("cold-current") || target.projectDir != root || target.sessionDir != desktopSessionDir(root) {
		t.Fatalf("cold task target lost its own identity or workspace: %+v", target)
	}
}

type taskKillController struct {
	*control.Controller
	mu     sync.Mutex
	killed []string
}

func newTaskKillController(t *testing.T, id string) *taskKillController {
	t.Helper()
	service, err := session.NewService(localDesktopHostID, session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := control.New(control.Options{SessionService: service, SessionCreateService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(func() {
		ctrl.Close()
		<-ctrl.Closed()
		if err := service.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return &taskKillController{Controller: ctrl}
}

func (c *taskKillController) TaskRuntimeOwnerID() string { return "test-recorder-owner" }

func (c *taskKillController) CancelJob(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killed = append(c.killed, id)
	return true
}

func (c *taskKillController) killCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.killed)
}

func (c *taskKillController) killedIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.killed...)
}

func TestTaskControlConcurrentInitializationReturnsOneService(t *testing.T) {
	app := &App{}
	const callers = 16
	services := make(chan *taskmonitor.ControlService, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			services <- app.taskControl()
		})
	}
	wg.Wait()
	close(services)

	var first *taskmonitor.ControlService
	for service := range services {
		if first == nil {
			first = service
			continue
		}
		if service != first {
			t.Fatal("taskControl returned more than one process-wide service")
		}
	}
}

func TestDesktopTaskJobKillerRoutesBySessionNotActiveTab(t *testing.T) {
	projectA := t.TempDir()
	projectB := t.TempDir()
	pathB := sessionRoute("shared-session")
	ctrlA := newTaskKillController(t, "shared-session")
	ctrlB := newTaskKillController(t, "shared-session")
	defer ctrlA.Close()
	defer ctrlB.Close()

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"active-a": {ID: "active-a", WorkspaceRoot: projectA, Ctrl: ctrlA},
		},
		detachedSessions: map[string]*WorkspaceTab{
			sessionRuntimeKey(pathB): {ID: "detached-b", WorkspaceRoot: projectB, Ctrl: ctrlB},
		},
		activeTabID: "active-a",
	}

	if controllerTaskSessionID(ctrlA) != controllerTaskSessionID(ctrlB) {
		t.Fatal("test setup must use matching opaque IDs in separate controller catalogs")
	}
	killer := desktopTaskJobKiller{app: app, projectDir: projectB}
	if !killer.Kill(controllerTaskSessionID(ctrlB), "task-1") {
		t.Fatal("expected detached project B task to be killed")
	}
	if ctrlA.killCount() != 0 || ctrlB.killCount() != 1 {
		t.Fatalf("kill routed incorrectly: active=%d detached=%d", ctrlA.killCount(), ctrlB.killCount())
	}
}

func TestDesktopTaskJobKillerRefusesLegacyTaskWithoutSession(t *testing.T) {
	root := t.TempDir()
	ctrl := newTaskKillController(t, "monitor-session")
	defer ctrl.Close()
	app := &App{tabs: map[string]*WorkspaceTab{"active": {ID: "active", WorkspaceRoot: root, Ctrl: ctrl}}}

	if (desktopTaskJobKiller{app: app, projectDir: root}).Kill("", "task-1") {
		t.Fatal("legacy task without session ID must not be routed by colliding task ID")
	}
	if ctrl.killCount() != 0 {
		t.Fatalf("legacy task unexpectedly killed %d runtime(s)", ctrl.killCount())
	}
}

func TestTaskMonitorUsesActiveWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"active": {ID: "active", Scope: "project", WorkspaceRoot: root},
		},
		activeTabID: "active",
	}
	if got := app.projectDir(); got != root {
		t.Fatalf("projectDir = %q, want active workspace %q", got, root)
	}
}

func TestTaskActionProjectResolvesAllowlistWithoutCatalog(t *testing.T) {
	root := t.TempDir()
	app := &App{
		ctx: context.Background(),
		tabs: map[string]*WorkspaceTab{
			"active": {ID: "active", Scope: "project", WorkspaceRoot: root},
		},
		activeTabID: "active",
	}
	key := taskcatalog.ProjectKey(root)
	project, err := app.taskActionProject(key)
	if err != nil {
		t.Fatalf("taskActionProject without catalog: %v", err)
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if project.Root != root || project.Key != key {
		t.Fatalf("project=%#v, want root=%q key=%q", project, root, key)
	}
	if _, err := app.taskActionProject("not-a-real-project-key"); err == nil {
		t.Fatal("expected unknown project key error")
	}
}

func TestStopTaskByKeyRoutesMonitorIdentityToRuntimeJob(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	ctrl := newTaskKillController(t, "monitor-session")
	defer ctrl.Close()
	app := &App{
		ctx: context.Background(),
		tabs: map[string]*WorkspaceTab{
			"active": {ID: "active", Scope: "project", WorkspaceRoot: root, Ctrl: ctrl},
		},
		activeTabID: "active",
	}
	sessionID := controllerTaskSessionID(ctrl)
	monitorID := sessionID + "--task-1"
	now := time.Now()
	if err := app.taskStore().SaveTask(app.ctx, root, taskmonitor.TaskSnapshot{
		SchemaVersion: 1, TaskID: monitorID, JobID: "task-1", SessionID: sessionID,
		State: taskmonitor.TaskStateRunning, RuntimeState: taskmonitor.RuntimeStateAlive, RuntimeOwnerID: ctrl.TaskRuntimeOwnerID(),
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := app.StopTaskByKey(TaskActionRequest{
		ProjectKey: taskcatalog.ProjectKey(root), TaskID: monitorID,
		ExpectedVersion: 1, IdempotencyKey: "desktop-route",
	})
	if err != nil || !res.Accepted {
		t.Fatalf("StopTaskByKey: result=%+v err=%v", res, err)
	}
	ids := ctrl.killedIDs()
	if len(ids) != 1 || ids[0] != "task-1" {
		t.Fatalf("runtime killed IDs = %v, want [task-1]", ids)
	}
}

func TestStopTaskByKeyKeepsSourceWorkspaceAfterActiveTabSwitch(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectA := t.TempDir()
	projectB := t.TempDir()
	if err := saveProjectsFile(desktopProjectFile{Projects: []desktopProject{{Root: projectA}, {Root: projectB}}}); err != nil {
		t.Fatal(err)
	}
	ctrlA := newTaskKillController(t, "shared-session")
	ctrlB := newTaskKillController(t, "shared-session")
	defer ctrlA.Close()
	defer ctrlB.Close()

	app := &App{
		ctx: context.Background(),
		tabs: map[string]*WorkspaceTab{
			"tab-a": {ID: "tab-a", Scope: "project", WorkspaceRoot: projectA, SessionID: "shared-session", Ctrl: ctrlA},
			"tab-b": {ID: "tab-b", Scope: "project", WorkspaceRoot: projectB, SessionID: "shared-session", Ctrl: ctrlB},
		},
		activeTabID: "tab-b",
	}
	sessionID := controllerTaskSessionID(ctrlA)
	if sessionID != controllerTaskSessionID(ctrlB) {
		t.Fatal("test setup must use matching opaque IDs in separate controller catalogs")
	}
	monitorID := sessionID + "--task-1"
	now := time.Now()
	for _, root := range []string{projectA, projectB} {
		if err := app.taskStore().SaveTask(app.ctx, root, taskmonitor.TaskSnapshot{
			SchemaVersion: 1, TaskID: monitorID, JobID: "task-1", SessionID: sessionID,
			State: taskmonitor.TaskStateRunning, RuntimeState: taskmonitor.RuntimeStateAlive, RuntimeOwnerID: ctrlA.TaskRuntimeOwnerID(),
			Version: 1, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := app.StopTaskByKey(TaskActionRequest{ProjectKey: taskcatalog.ProjectKey(projectA), TaskID: monitorID, ExpectedVersion: 1, IdempotencyKey: "project-bound-stop"})
	if err != nil || !res.Accepted {
		t.Fatalf("StopTaskByKey: result=%+v err=%v", res, err)
	}
	if ctrlA.killCount() != 1 || ctrlB.killCount() != 0 {
		t.Fatalf("kill routed away from source tab: projectA=%d projectB=%d", ctrlA.killCount(), ctrlB.killCount())
	}
	projectBTask, err := app.taskStore().GetTask(app.ctx, projectB, monitorID)
	if err != nil || projectBTask == nil || projectBTask.State != taskmonitor.TaskStateRunning {
		t.Fatalf("project B task mutated by project A control: task=%+v err=%v", projectBTask, err)
	}
}
