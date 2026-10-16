package main

import (
	"context"
	"sync"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

type delayedContextController struct {
	control.SessionAPI
	owner            *control.Controller
	once             sync.Once
	entered, release chan struct{}
}

func (c *delayedContextController) SessionRef() (session.SessionRef, bool) {
	ref, bound := c.owner.SessionRef()
	c.once.Do(func() { close(c.entered); <-c.release })
	return ref, bound
}

func currentContextController(t *testing.T, app *App, id string) *control.Controller {
	t.Helper()
	service := app.desktopSessionService("")
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.New(usageProvider{}, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: ag, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(func() { ctrl.Close(); <-ctrl.Closed() })
	return ctrl
}

func TestContextUsageForTabReadsRotatedSessionWithoutMutatingTelemetry(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	t.Cleanup(app.closeSessionServices)
	ctrl := currentContextController(t, app, "rotated")
	tab := &WorkspaceTab{ID: "tab", Ctrl: ctrl, SessionID: "rotated"}
	app.tabs[tab.ID] = tab
	tab.syncTelemetryToSession(sessionRoute("stale"))
	tab.recordUsage(costedUsageEvent())
	info := app.ContextUsageForTab(tab.ID)
	if info.SessionCost != 0 || info.SessionTokens != 0 {
		t.Fatalf("rotated session inherited old counters: %+v", info)
	}
	if got := tab.telemetrySnapshot().Usage.RequestCount; got != 1 {
		t.Fatalf("read mutated telemetry: %d", got)
	}
	tab.syncTelemetryToSession(sessionRoute("rotated"))
	if got := tab.telemetrySnapshot().Usage.RequestCount; got != 0 {
		t.Fatalf("writer did not reset counters: %d", got)
	}
}

func TestContextReadsCannotRestorePreviousSessionTelemetry(t *testing.T) {
	for _, panel := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			name := "usage"
			if panel {
				name = "panel"
			}
			if replace {
				name += "/replacement"
			} else {
				name += "/rotation"
			}
			t.Run(name, func(t *testing.T) {
				isolateDesktopUserDirs(t)
				app := NewApp()
				app.ctx = t.Context()
				t.Cleanup(app.closeSessionServices)
				original := currentContextController(t, app, "old")
				old := &delayedContextController{SessionAPI: original, owner: original, entered: make(chan struct{}), release: make(chan struct{})}
				tab := &WorkspaceTab{ID: "tab", Ctrl: old, SessionID: "old"}
				app.tabs[tab.ID] = tab
				tab.syncTelemetryToSession(sessionRoute("old"))
				tab.recordUsage(costedUsageEvent())
				if err := saveTelemetryFor(sessionRoute("old"), tab.telemetrySnapshot()); err != nil {
					t.Fatal(err)
				}
				done := make(chan int, 1)
				go func() {
					if panel {
						done <- app.ContextPanel(tab.ID).TotalTokens
					} else {
						done <- app.ContextUsageForTab(tab.ID).SessionTokens
					}
				}()
				<-old.entered
				if replace {
					next := currentContextController(t, app, "new")
					app.mu.Lock()
					tab.Ctrl = next
					tab.SessionID = "new"
					tab.SessionGeneration++
					app.mu.Unlock()
				} else {
					service := app.desktopSessionService("")
					runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "new"})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := original.OpenSession(context.Background(), runtime.Ref()); err != nil {
						t.Fatal(err)
					}
				}
				tab.resetTelemetry(sessionRoute("new"))
				close(old.release)
				if got := <-done; got != 0 {
					t.Fatalf("stale response contains %d previous-session tokens", got)
				}
				if got := tab.telemetrySnapshot().Usage; got.TotalTokens != 0 || got.SessionCost != 0 || got.RequestCount != 0 {
					t.Fatalf("old read overwrote new telemetry: %+v", got)
				}
				tab.telemMu.Lock()
				key := tab.telemetrySessionKey
				tab.telemMu.Unlock()
				if key != sessionRuntimeKey(sessionRoute("new")) {
					t.Fatalf("telemetry owner=%q", key)
				}
			})
		}
	}
}
