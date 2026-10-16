package control

import (
	"context"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func TestBranchResetsTwoModelPlannerContext(t *testing.T) {
	planner := &recordingProvider{name: "planner", streams: [][]provider.Chunk{
		planTurn("OLD PLAN: inspect alpha.go"),
		planTurn("BRANCH PLAN: inspect beta.go"),
	}}
	execProv := &recordingProvider{name: "executor", streams: [][]provider.Chunk{
		textTurn("old done"),
		textTurn("branch done"),
	}}
	exec := agent.New(execProv, tool.NewRegistry(), agent.NewSession("exec sys"), agent.Options{}, event.Discard)
	coord := agent.NewCoordinatorWithPlannerPolicy(planner, agent.NewSession("planner sys"), nil, agent.PlannerToolRegistry(tool.NewRegistry()), agent.Options{}, exec, 0, event.Discard, nil)
	c, _ := bindCurrentBranchController(t, Options{Runner: coord, Executor: exec, SystemPrompt: "exec sys", Label: "test"})

	if err := c.Run(context.Background(), "old task alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Branch("child"); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(context.Background(), "branch task beta"); err != nil {
		t.Fatal(err)
	}

	if len(planner.requests) != 2 {
		t.Fatalf("planner requests = %d, want 2", len(planner.requests))
	}
	second := requestMessagesText(planner.requests[1].Messages)
	if strings.Contains(second, "old task alpha") || strings.Contains(second, "OLD PLAN") {
		t.Fatalf("branch planner request leaked previous session context:\n%s", second)
	}
	if !strings.Contains(second, "branch task beta") {
		t.Fatalf("branch planner request missing current task:\n%s", second)
	}
}

func TestSwitchBranchResetsTwoModelPlannerContext(t *testing.T) {
	planner := &recordingProvider{name: "planner", streams: [][]provider.Chunk{
		planTurn("ROOT PLAN: inspect alpha.go"),
		planTurn("CHILD PLAN: inspect beta.go"),
		planTurn("ROOT AGAIN PLAN: inspect gamma.go"),
	}}
	execProv := &recordingProvider{name: "executor", streams: [][]provider.Chunk{
		textTurn("root done"),
		textTurn("child done"),
		textTurn("root again done"),
	}}
	exec := agent.New(execProv, tool.NewRegistry(), agent.NewSession("exec sys"), agent.Options{}, event.Discard)
	coord := agent.NewCoordinatorWithPlannerPolicy(planner, agent.NewSession("planner sys"), nil, agent.PlannerToolRegistry(tool.NewRegistry()), agent.Options{}, exec, 0, event.Discard, nil)
	c, _ := bindCurrentBranchController(t, Options{Runner: coord, Executor: exec, SystemPrompt: "exec sys", Label: "test"})
	t.Cleanup(c.Close)

	if err := c.Run(context.Background(), "root task alpha"); err != nil {
		t.Fatal(err)
	}
	rootID := c.CurrentBranchID()
	if _, err := c.Branch("child"); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(context.Background(), "child task beta"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SwitchBranch(rootID); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(context.Background(), "root task gamma"); err != nil {
		t.Fatal(err)
	}

	if len(planner.requests) != 3 {
		t.Fatalf("planner requests = %d, want 3", len(planner.requests))
	}
	third := requestMessagesText(planner.requests[2].Messages)
	if strings.Contains(third, "child task beta") || strings.Contains(third, "CHILD PLAN") {
		t.Fatalf("switched planner request leaked previous branch context:\n%s", third)
	}
	if !strings.Contains(third, "root task gamma") {
		t.Fatalf("switched planner request missing current task:\n%s", third)
	}
}

func TestSubmitBranchHonorsNumericTurnTarget(t *testing.T) {
	for _, target := range []struct {
		command, title string
		turns          int
	}{
		{"/branch 1 first", "first", 0}, {"/branch 2 experiment", "experiment", 1},
	} {
		t.Run(target.title, func(t *testing.T) {
			service, _, c := newForkTargetsHarness(t, "submit-parent", testutil.Turn{Text: "first answer"}, testutil.Turn{Text: "second answer"})
			for _, prompt := range []string{"first prompt", "second prompt"} {
				if err := c.RunTurn(t.Context(), prompt); err != nil {
					t.Fatal(err)
				}
			}
			parent, _ := c.SessionRef()
			c.Submit(target.command)
			child, ok := c.SessionRef()
			if !ok || child == parent {
				t.Fatal("numeric branch did not switch session")
			}
			snapshot, err := service.Query().Snapshot(t.Context(), child)
			if err != nil || len(snapshot.Projection.Turns) != target.turns || snapshot.Projection.Title != target.title {
				t.Fatalf("branch snapshot = %+v, %v", snapshot.Projection, err)
			}
			original, err := service.Query().Snapshot(t.Context(), parent)
			if err != nil || len(original.Projection.Turns) != 2 {
				t.Fatalf("branch altered parent: %v", err)
			}
		})
	}
}

func TestParseBranchTarget(t *testing.T) {
	turn, name, fromTurn, err := ParseBranchTarget("3 experiment")
	if err != nil || !fromTurn || turn != 3 || name != "experiment" {
		t.Fatalf("ParseBranchTarget numeric = (%d,%q,%v,%v)", turn, name, fromTurn, err)
	}
	turn, name, fromTurn, err = ParseBranchTarget("experiment")
	if err != nil || fromTurn || turn != 0 || name != "experiment" {
		t.Fatalf("ParseBranchTarget name = (%d,%q,%v,%v)", turn, name, fromTurn, err)
	}
	if _, _, _, err = ParseBranchTarget("0 bad"); err == nil {
		t.Fatal("ParseBranchTarget should reject non-positive turns")
	}
}

func TestSubmitSwitchEmitsErrorNotice(t *testing.T) {
	var notices []string
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{
		Executor: exec,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.Notice {
				notices = append(notices, e.Text)
			}
		}),
	})

	c.Submit("/switch")
	if len(notices) == 0 {
		t.Fatal("/switch with empty ref should emit an error notice")
	}
	if !strings.Contains(notices[len(notices)-1], "usage") {
		t.Fatalf("notice = %q, want usage hint", notices[len(notices)-1])
	}

	notices = notices[:0]
	c.Submit("/switch nonexistent")
	if len(notices) == 0 {
		t.Fatal("/switch with unknown ref should emit an error notice")
	}
}

func TestSubmitBranchEmitsErrorNoticeWhileRunning(t *testing.T) {
	var notices []string
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{
		Executor:   exec,
		SessionDir: t.TempDir(),
		Label:      "test",
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.Notice {
				notices = append(notices, e.Text)
			}
		}),
	})

	c.mu.Lock()
	c.turns.phase = session.RuntimeRunning
	c.mu.Unlock()

	c.Submit("/branch experiment")
	if len(notices) == 0 {
		t.Fatal("/branch while running should emit an error notice")
	}
	if !strings.Contains(notices[len(notices)-1], "cannot branch") {
		t.Fatalf("notice = %q, want 'cannot branch' error", notices[len(notices)-1])
	}
}

func TestFormatBranchTreeMarksCurrent(t *testing.T) {
	branches := []BranchInfo{
		{ID: "root", Preview: "root", Turns: 1},
		{ID: "child", ParentID: "root", Name: "child branch", Turns: 2},
	}
	got := FormatBranchTree(branches, "child")
	if !strings.Contains(got, "child branch  2 turns  current") {
		t.Fatalf("tree should mark current branch:\n%s", got)
	}
	if strings.Contains(got, "*") {
		t.Fatalf("tree should not use duplicate current markers:\n%s", got)
	}
}

func TestFormatBranchTreeUsesCompactVisualRows(t *testing.T) {
	branches := []BranchInfo{
		{
			ID:      "session-root-0123456789abcdef",
			Preview: "你是谁",
			Turns:   3,
		},
		{
			ID:       "session-child-0123456789abcdef",
			ParentID: "session-root-0123456789abcdef",
			Preview:  `{ "code": 0, "msg": "success", "data": { "rows": [] } }`,
			Turns:    1,
		},
	}
	got := FormatBranchTree(branches, "session-child-0123456789abcdef")
	checks := []string{
		"└─",
		"session-child-012...",
		"JSON response: success",
		"1 turn",
		"current",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("tree missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "session-child-0123456789abcdef") {
		t.Fatalf("tree should use compact branch IDs:\n%s", got)
	}
	if strings.Contains(got, `"data"`) {
		t.Fatalf("tree should summarize JSON-like previews:\n%s", got)
	}
}

func TestResolveBranchAcceptsDisplayedShortID(t *testing.T) {
	branches := []BranchInfo{
		{ID: "session-child-0123456789abcdef"},
	}
	got, err := resolveBranch(branches, "session-child-012...")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != branches[0].ID {
		t.Fatalf("branch = %q, want %q", got.ID, branches[0].ID)
	}
}
