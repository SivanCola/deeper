package control

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/evidence"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"

	"deepzora/internal/testutil"
	_ "deepzora/internal/tool/builtin"
)

// goalRegistry returns a registry carrying the update_goal builtin so scripted
// goal turns can report dispositions through the structured tool.
func goalRegistry() *tool.Registry {
	reg := tool.NewRegistry()
	if t, ok := tool.LookupBuiltin("update_goal"); ok {
		reg.Add(t)
	}
	return reg
}

// goalWireStatus maps FSM status values to the update_goal wire enum.
func goalWireStatus(status string) string {
	switch status {
	case GoalStatusComplete:
		return "complete"
	case GoalStatusBlocked:
		return "blocked"
	default:
		return "continue"
	}
}

// goalToolTurn models one goal turn's provider sequence: the model calls
// update_goal with the given disposition, then answers with text. Call IDs are
// unique per call so recycled scripted turns never collide in the transcript.
func goalToolTurn(status, reason, nextAction string) [][]provider.Chunk {
	args, err := json.Marshal(map[string]string{"status": goalWireStatus(status), "reason": reason, "next_action": nextAction})
	if err != nil {
		panic(err)
	}
	id := fmt.Sprintf("ug-%d", goalToolCallSeq.Add(1))
	return [][]provider.Chunk{
		{testutil.ToolCallChunk(id, "update_goal", string(args)), {Type: provider.ChunkDone}},
		textTurn("worked on the goal"),
	}
}

var goalToolCallSeq atomic.Uint64

// flattenTurns concatenates per-goal-turn provider sequences into one flat
// scripted provider stream.
func flattenTurns(groups ...[][]provider.Chunk) [][]provider.Chunk {
	var out [][]provider.Chunk
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func TestActiveGoalBlockCarriesTaskContractAndPausePolicy(t *testing.T) {
	block := activeGoalBlock("fix the parser")
	for _, want := range []string{
		"Treat the user's goal as a task contract",
		"Context, Request, Output format, Constraints",
		"Pause policy",
		"irreversible or externally visible operation",
		"the requested scope has changed",
		"information only the user can provide",
		"output format and constraints are satisfied",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("active goal block missing %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "AutoResearch protocol") {
		t.Fatalf("simple goal should not include AutoResearch protocol:\n%s", block)
	}
}

func TestPlainInputWithStrongResearchSignalStaysNormal(t *testing.T) {
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		textTurn("Here is the normal response."),
	}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession(""), agent.Options{}, event.Discard)
	events := make(chan event.Event, 8)
	c := newOwnedTestController(t, Options{
		Runner:   ag,
		Executor: ag,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone || e.Kind == event.Notice {
				events <- e
			}
		}),
	})

	c.Submit("持续排查这个线上卡顿直到根因明确，并验证修复")
	waitForTurnDone(t, events)

	if prov.call != 1 {
		t.Fatalf("provider calls = %d, want 1", prov.call)
	}
	first := agent.StripTransientUserBlocks(firstUserMessage(ag.Session().Messages))
	if !strings.HasSuffix(first, "持续排查这个线上卡顿直到根因明确，并验证修复") {
		t.Fatalf("ordinary turn should preserve the original prompt suffix: %q", first)
	}
	if strings.Contains(first, "<active-goal>") || strings.Contains(first, "AutoResearch protocol") {
		t.Fatalf("ordinary prompt should not enter Goal or AutoResearch:\n%s", first)
	}
	if got := c.GoalStatus(); got != GoalStatusStopped {
		t.Fatalf("GoalStatus() = %q, want stopped", got)
	}
}

func TestPlainInputWithStrongResearchSignalPreservesRefsWithoutStartingGoal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("important referenced evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		textTurn("Referenced normal response."),
	}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession(""), agent.Options{}, event.Discard)
	events := make(chan event.Event, 8)
	c := newOwnedTestController(t, Options{
		WorkspaceRoot: root,
		Runner:        ag,
		Executor:      ag,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone || e.Kind == event.Notice {
				events <- e
			}
		}),
	})

	c.Submit("持续排查直到根因明确，并验证 @notes.txt")
	waitForTurnDone(t, events)

	first := firstUserMessage(ag.Session().Messages)
	for _, want := range []string{
		"important referenced evidence",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("ordinary turn with refs missing %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "<active-goal>") || strings.Contains(first, "AutoResearch protocol") {
		t.Fatalf("ordinary prompt with refs should not enter Goal or AutoResearch:\n%s", first)
	}
	if got := c.GoalStatus(); got != GoalStatusStopped {
		t.Fatalf("GoalStatus() = %q, want stopped", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".deepzora", "autoresearch")); !os.IsNotExist(err) {
		t.Fatalf("ordinary prompt created AutoResearch state: err=%v", err)
	}
}

func TestResearchGoalUsesContinuousRuntimeWithoutArchive(t *testing.T) {
	root := t.TempDir()
	service, err := session.NewService("test", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })

	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{WorkspaceRoot: root, SessionDir: root, Executor: exec, SessionService: service, ExclusiveSession: true})
	if _, err := c.BindFreshSession(t.Context(), "goal"); err != nil {
		t.Fatal(err)
	}
	c.SetGoal("fix the typo and add a test")
	defer c.Close()

	if _, err := os.Stat(filepath.Join(root, ".deepzora", "autoresearch")); !os.IsNotExist(err) {
		t.Fatalf("research Goal created legacy archive: %v", err)
	}
	if composed := c.Compose("continue"); strings.Contains(composed, "AutoResearch") || strings.Contains(composed, "autoresearch") {
		t.Fatalf("Goal prompt exposes removed AutoResearch protocol:\n%s", composed)
	}
}

func TestPlainInputWithWeakResearchSignalStaysNormal(t *testing.T) {
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		textTurn("Here is a normal answer."),
	}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession(""), agent.Options{}, event.Discard)
	events := make(chan event.Event, 4)
	c := newOwnedTestController(t, Options{
		Runner:   ag,
		Executor: ag,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				events <- e
			}
		}),
	})

	c.Submit("长期来看这个模块怎么优化？")
	waitForTurnDone(t, events)

	first := firstUserMessage(ag.Session().Messages)
	if strings.Contains(first, "<active-goal>") || strings.Contains(first, "AutoResearch protocol") {
		t.Fatalf("ordinary prompt should stay outside Goal and AutoResearch:\n%s", first)
	}
	if got := c.GoalStatus(); got != GoalStatusStopped {
		t.Fatalf("GoalStatus() = %q, want stopped", got)
	}
}

func TestCancelStopsIdleGoalWithIncompleteTodos(t *testing.T) {
	ag := agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard)
	ag.SeedTodoState([]evidence.TodoItem{{Content: "finish the migration", Status: "in_progress"}})
	c := newOwnedTestController(t, Options{Executor: ag, Sink: event.Discard})
	c.SetGoal("finish the migration")

	c.Cancel()

	if got := c.GoalStatus(); got != GoalStatusStopped {
		t.Fatalf("GoalStatus() = %q, want stopped", got)
	}
	if got := c.Goal(); got != "finish the migration" {
		t.Fatalf("Goal() = %q, want stopped goal text to remain for display/persistence", got)
	}
	if todos := c.Todos(); len(todos) != 0 {
		t.Fatalf("Todos() after stopping idle goal = %+v, want executor seed ignored", todos)
	}
}

// TestSessionRotationClearsActiveGoal pins the /new & /clear goal semantics:
// a fresh session starts with no active goal (so the old goal's text stops
// injecting into its first turns), while the OLD session's persisted
// current event log retains the old goal for an explicit later resume.
func TestSessionRotationClearsActiveGoal(t *testing.T) {
	dir := t.TempDir()
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := newCurrentOwnedTestController(t, Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, Label: "test"})

	before, _ := c.SessionRef()
	c.SetGoal("ship the release checklist")
	if got := c.Goal(); got != "ship the release checklist" {
		t.Fatalf("Goal() = %q after SetGoal", got)
	}
	if composed := c.Compose("hello"); !strings.Contains(composed, "<active-goal>") {
		t.Fatalf("running goal should inject into turns, composed = %q", composed)
	}

	if err := c.NewSession(); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := c.Goal(); got != "" {
		t.Fatalf("Goal() after /new = %q, want empty", got)
	}
	if composed := c.Compose("hello"); strings.Contains(composed, "<active-goal>") {
		t.Fatalf("old goal leaked into the fresh session's turn: %q", composed)
	}
	oldState, err := c.SessionService().Query().Snapshot(t.Context(), before)
	if err != nil || !strings.Contains(string(oldState.Projection.GoalState), "ship the release checklist") {
		t.Fatalf("old goal was not retained: %s, %v", oldState.Projection.GoalState, err)
	}
	after, _ := c.SessionRef()
	newState, err := c.SessionService().Query().Snapshot(t.Context(), after)
	if err != nil || strings.Contains(string(newState.Projection.GoalState), "ship the release checklist") {
		t.Fatalf("fresh goal leaked: %s, %v", newState.Projection.GoalState, err)
	}

	// Same contract for /clear.
	c.SetGoal("another goal")
	if err := c.ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	if got := c.Goal(); got != "" {
		t.Fatalf("Goal() after /clear = %q, want empty", got)
	}
	if composed := c.Compose("hello"); strings.Contains(composed, "<active-goal>") {
		t.Fatalf("old goal leaked into the cleared session's turn: %q", composed)
	}
}
