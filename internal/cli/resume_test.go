package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
)

// TestResumeDispatchOpensPicker proves bare "/resume" opens the interactive
// picker without duplicating the same list in transcript scrollback.
func TestResumeDispatchOpensPicker(t *testing.T) {
	dir := t.TempDir()
	createCanonicalTestSession(t, session.RootForSessionDir(dir), "a", "alpha prompt")
	waitForCatalogMetadata(t, dir, "a")
	createCanonicalTestSession(t, session.RootForSessionDir(dir), "b", "beta prompt")
	waitForCatalogMetadata(t, dir, "b")

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.width = 80
	m.ctrl = newOwnedTestController(t, control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	if cmd := m.runSlashCommand("/resume"); cmd != nil {
		t.Fatal("/resume should not return a tea.Cmd")
	}
	if m.resumePick == nil {
		t.Fatal("bare /resume should open the picker")
	}
	if len(m.resumePick.entries) != 2 {
		t.Fatalf("picker should have 2 sessions, got %d", len(m.resumePick.entries))
	}
	out := strings.Join(m.transcript, "\n")
	if strings.Contains(out, "alpha prompt") || strings.Contains(out, "beta prompt") {
		t.Fatalf("picker previews should not be duplicated in scrollback:\n%s", out)
	}
}

// TestResumePickerNavigateAndSelect proves the picker's up/down navigation and
// Enter to resume the selected session.
func TestResumePickerNavigateAndSelect(t *testing.T) {
	dir := t.TempDir()
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)

	createCanonicalTestSession(t, session.RootForSessionDir(dir), "first", "first session prompt")
	createCanonicalTestSession(t, session.RootForSessionDir(dir), "second", "SECOND-SESSION-PROMPT")
	waitForCatalogMetadata(t, dir, "first", "second")
	ctrl := newOwnedTestController(t, control.Options{Executor: exec, SessionDir: dir, Label: "test", SessionService: testCLISessionService(t, dir), ExclusiveSession: true})

	m := newTestChatTUI()
	m.width = 80
	m.ctrl = ctrl

	// Open the picker via bare /resume.
	m.runSlashCommand("/resume")
	if m.resumePick == nil {
		t.Fatal("bare /resume should open the picker")
	}
	if len(m.resumePick.entries) != 2 {
		t.Fatalf("picker should have 2 sessions, got %d", len(m.resumePick.entries))
	}

	// The first session (default selection) is the most recent, which is b.jsonl.
	// Press Enter to resume it.
	next, _ := m.handleResumePickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(chatTUI)

	if ref, bound := ctrl.SessionRef(); !bound || ref.SessionID != "second" {
		t.Fatalf("session identity = %+v, %v, want second", ref, bound)
	}
	if out := strings.Join(m.transcript, "\n"); !strings.Contains(out, "SECOND-SESSION-PROMPT") {
		t.Fatalf("transcript should replay the resumed session:\n%s", out)
	}
	if m.resumePick != nil {
		t.Fatal("picker should close after resume")
	}
}

// TestResumePickerEscDismisses proves pressing Esc closes the picker without
// switching sessions.
func TestResumePickerEscDismisses(t *testing.T) {
	dir := t.TempDir()
	createCanonicalTestSession(t, session.RootForSessionDir(dir), "a", "alpha prompt")
	waitForCatalogMetadata(t, dir, "a")

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.ctrl = newOwnedTestController(t, control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	m.runSlashCommand("/resume")
	if m.resumePick == nil {
		t.Fatal("bare /resume should open the picker")
	}

	next, _ := m.handleResumePickerKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next.(chatTUI)
	if m.resumePick != nil {
		t.Fatal("picker should close on Esc")
	}
}

// writeOldTranscriptFixture supplies rejected historical input without using
// a retired production writer.
func writeOldTranscriptFixture(t *testing.T, path, prompt string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"role": "user", "content": prompt})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestResumeArgCompletionListsSessions proves "/resume " opens an indexed menu
// of the saved sessions, mirroring the /switch branch completion.
func TestResumeArgCompletionListsSessions(t *testing.T) {
	dir := t.TempDir()
	createCanonicalTestSession(t, session.RootForSessionDir(dir), "a", "first")
	waitForCatalogMetadata(t, dir, "a")
	createCanonicalTestSession(t, session.RootForSessionDir(dir), "b", "second")
	waitForCatalogMetadata(t, dir, "b")

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.ctrl = newOwnedTestController(t, control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	m.input.SetValue("/resume ")
	m.updateCompletion()
	if !m.completion.active || m.completion.kind != compSlashArg {
		t.Fatalf("/resume should open argument completion: %+v", m.completion)
	}
	if got := labels(m.completion.items); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("resume completion = %v, want [1 2]", got)
	}
}

// TestResumeAcceptChainsIntoSessionMenu proves accepting "/resume" (a
// non-descend command that still takes arguments) immediately opens the session
// menu, rather than waiting for the next keystroke.
func TestResumeAcceptChainsIntoSessionMenu(t *testing.T) {
	dir := t.TempDir()
	createCanonicalTestSession(t, session.RootForSessionDir(dir), "a", "first")
	waitForCatalogMetadata(t, dir, "a")

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.ctrl = newOwnedTestController(t, control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	m.input.SetValue("/resu")
	m.updateCompletion()
	m.acceptCompletion()
	if got := m.input.Value(); got != "/resume " {
		t.Fatalf("accepting /resume should fill %q, got %q", "/resume ", got)
	}
	if !m.completion.active || m.completion.kind != compSlashArg {
		t.Fatalf("accepting /resume should chain into the session menu: %+v", m.completion)
	}
}
