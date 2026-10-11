package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/provider"
)

// appendHistoryTestSessionTurn writes one more turn to a session's durable log
// while a switch into that session is in flight. A switch that re-reads the log
// to build its first screen surfaces the appended turn; one that reuses the
// transcript it already loaded for the rebind does not.
func appendHistoryTestSessionTurn(t *testing.T, path, prompt string) {
	t.Helper()
	session, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	session.Add(provider.Message{Role: provider.RoleUser, Content: prompt})
	if err := session.Save(path); err != nil {
		t.Fatalf("save %s: %v", path, err)
	}
}

// appendOnCommit makes the durable log grow once the switch has already loaded
// it, so a page built from a second read would show the extra turn.
func appendOnCommit(t *testing.T, app *App, path, prompt string) {
	t.Helper()
	app.rebindCandidateHook = func(stage string) error {
		if stage == "committed" {
			appendHistoryTestSessionTurn(t, path, prompt)
		}
		return nil
	}
}

func requireSingleDurableRead(t *testing.T, page HistoryPage) {
	t.Helper()
	if page.Switch == nil {
		t.Fatal("switch page carries no phase breakdown")
	}
	if page.Switch.Outcome != "ok" {
		t.Fatalf("switch outcome = %q, want ok", page.Switch.Outcome)
	}
	if page.Switch.DurableReads != 1 {
		t.Fatalf("switch durable reads = %d, want 1", page.Switch.DurableReads)
	}
	if page.Switch.LoadedCount == 0 || page.Switch.LoadedBytes == 0 {
		t.Fatalf("switch phase counts missing: %+v", page.Switch)
	}
}

func requireHistoryPagesMatch(t *testing.T, want, got HistoryPage, label string) {
	t.Helper()
	if got.StartTurn != want.StartTurn || got.EndTurn != want.EndTurn ||
		got.TotalTurns != want.TotalTurns || got.HasOlder != want.HasOlder {
		t.Fatalf("%s window = %d-%d/%d older=%v, want %d-%d/%d older=%v", label,
			got.StartTurn, got.EndTurn, got.TotalTurns, got.HasOlder,
			want.StartTurn, want.EndTurn, want.TotalTurns, want.HasOlder)
	}
	if got.Digest != want.Digest {
		t.Fatalf("%s digest = %q, want %q", label, got.Digest, want.Digest)
	}
	if got.Revision != want.Revision {
		t.Fatalf("%s revision = %d, want %d", label, got.Revision, want.Revision)
	}
	wantJSON, err := json.Marshal(want.Messages)
	if err != nil {
		t.Fatalf("marshal want messages: %v", err)
	}
	gotJSON, err := json.Marshal(got.Messages)
	if err != nil {
		t.Fatalf("marshal got messages: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s messages differ:\n got %s\nwant %s", label, gotJSON, wantJSON)
	}
}

// resumedCleanTarget writes a target session whose persisted system prompt is
// already the composed one. The rebound controller is then content-identical to
// the log, so a switch that re-reads the log is the only way the page can pick
// up a write that lands after the load.
func resumedCleanTarget(t *testing.T, app *App, tab *WorkspaceTab, name string) string {
	t.Helper()
	dir := filepath.Dir(tab.currentSessionPath())
	probe := filepath.Join(dir, name+".probe.jsonl")
	writeHistoryTestSession(t, probe, "probe prompt")
	if _, err := app.ResumeSessionPageForTab(tab.ID, probe, defaultHistoryPageTurns); err != nil {
		t.Fatalf("probe switch: %v", err)
	}
	prompt := systemPromptFrom(app.controllerForTab(tab).History())
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("rebuilt controller composed no system prompt")
	}
	path := filepath.Join(dir, name)
	session := agent.NewSession(prompt)
	session.Add(provider.Message{Role: provider.RoleUser, Content: "target prompt"})
	if err := session.Save(path); err != nil {
		t.Fatalf("save %s: %v", path, err)
	}
	return path
}

func requireHistoryTurnCount(t *testing.T, page HistoryPage, want int, label string) {
	t.Helper()
	if page.TotalTurns != want {
		t.Fatalf("%s totalTurns = %d, want %d", label, page.TotalTurns, want)
	}
}

// newRebindTestApp builds the smallest App a switch needs: one global-scope tab
// with a live controller, its session lease, a published runtime, and a ready
// sink.
func newRebindTestApp(t *testing.T, root, sessionPath string, ctrl control.SessionAPI, tabID string) *App {
	t.Helper()
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID:            tabID,
		Scope:         "global",
		WorkspaceRoot: root,
		SessionPath:   sessionPath,
		Ctrl:          ctrl,
		Ready:         true,
		sink:          &tabEventSink{tabID: tabID, app: app, ctx: app.ctx},
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	if err := tab.ensureSessionLease(sessionPath); err != nil {
		t.Fatalf("lease session: %v", err)
	}
	app.mu.Lock()
	app.newSessionRuntimeLocked(tab, sessionRuntimeKey(sessionPath))
	app.advanceSessionRuntimeEpochLocked(tab)
	app.mu.Unlock()
	t.Cleanup(func() {
		if live := app.controllerForTab(tab); live != nil {
			live.Close()
		}
		tab.releaseSessionLease()
	})
	return app
}
