package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	goaldomain "deepzora/internal/goal"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
	"deepzora/internal/transcript"
)

func TestHistoryMessagesIncludeAssistantReasoning(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "expanded prompt", CreatedAt: 1_718_000_000_000},
		{Role: provider.RoleAssistant, Content: "answer", ReasoningContent: "thinking trace", WorkDurationMs: 24_000, ToolCalls: []provider.ToolCall{{
			ID: "call_1", Name: "bash", Arguments: `{"command":"pwd"}`,
		}}, MemoryCitations: []provider.MemoryCitation{{
			ID: "mem-1", Source: "Memory v5", Note: "use previous bash failure", Kind: "constraint",
		}}},
		{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_1", Content: "tool output", ReasoningContent: "ignored by frontend filter"},
		{Role: provider.RoleAssistant, ReasoningContent: "tool-call-only thinking"},
	}

	got := historyMessages(msgs, func(content string) string {
		if content != "expanded prompt" {
			t.Fatalf("unexpected user content passed to resolver: %q", content)
		}
		return "display prompt"
	})

	if len(got) != len(msgs) {
		t.Fatalf("history length = %d, want %d", len(got), len(msgs))
	}
	if got[0].Content != "display prompt" {
		t.Fatalf("user display content = %q, want display prompt", got[0].Content)
	}
	if got[0].SubmitText != "expanded prompt" {
		t.Fatalf("user submit text = %q, want expanded prompt", got[0].SubmitText)
	}
	if got[0].CreatedAt != 1_718_000_000_000 {
		t.Fatalf("user createdAt = %d, want 1718000000000", got[0].CreatedAt)
	}
	if got[1].Reasoning != "thinking trace" {
		t.Fatalf("assistant reasoning = %q, want thinking trace", got[1].Reasoning)
	}
	if got[1].WorkDurationMs != 24_000 {
		t.Fatalf("assistant work duration = %d, want 24000", got[1].WorkDurationMs)
	}
	if len(got[1].MemoryCitations) != 1 || got[1].MemoryCitations[0].Note != "use previous bash failure" {
		t.Fatalf("assistant memory citations not preserved: %+v", got[1].MemoryCitations)
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "call_1" || got[1].ToolCalls[0].Name != "bash" {
		t.Fatalf("assistant tool calls not preserved: %+v", got[1].ToolCalls)
	}
	if !got[1].ToolCalls[0].ArgumentsArchived || got[1].ToolCalls[0].Arguments != "" || got[1].ToolCalls[0].Subject != "pwd" {
		t.Fatalf("assistant tool call was not restored as lightweight metadata: %+v", got[1].ToolCalls[0])
	}
	if got[2].ToolCallID != "call_1" || got[2].ToolName != "bash" || got[2].Content != "" || !got[2].ToolResultArchived {
		t.Fatalf("tool result details not preserved: %+v", got[2])
	}
	if got[2].Reasoning != "" {
		t.Fatalf("non-assistant reasoning should stay hidden, got %q", got[2].Reasoning)
	}
	if got[3].Reasoning != "tool-call-only thinking" {
		t.Fatalf("empty-content assistant reasoning = %q, want tool-call-only thinking", got[3].Reasoning)
	}
}

func TestHistoryMessagesReplayAttachedDecisionReceiptAfterAssistant(t *testing.T) {
	receipt := &provider.DecisionReceipt{ID: "approval-1", Kind: "tool", Tool: "bash", Outcome: "allow_once"}
	got := historyMessages([]provider.Message{
		{Role: provider.RoleUser, Content: "run it"},
		{
			Role:             provider.RoleAssistant,
			ToolCalls:        []provider.ToolCall{{ID: "call-1", Name: "bash", Arguments: `{}`}},
			DecisionReceipts: []*provider.DecisionReceipt{receipt},
		},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "bash", Content: "ok"},
	}, func(content string) string { return content })

	if len(got) != 4 {
		t.Fatalf("history messages = %d, want user, assistant, receipt, tool: %+v", len(got), got)
	}
	if len(got[1].ToolCalls) != 1 || got[2].Code != event.NoticeCodeDecisionReceipt || got[2].DecisionReceipt == nil {
		t.Fatalf("history did not replay the decision after its assistant call: %+v", got)
	}
	if got[3].Role != "tool" || got[3].ToolCallID != "call-1" || !got[3].ToolResultArchived {
		t.Fatalf("history lost the actual tool result: %+v", got[3])
	}
}

func TestHistoryMessagesPreferPersistedRawUserContent(t *testing.T) {
	const raw = "fix the bug"
	const rendered = "<capability-route version=\"1\">\nuse review\n</capability-route>\n\nfix the bug"
	msgs := []provider.Message{{Role: provider.RoleUser, Content: rendered, RawContent: raw}}

	got := historyMessages(msgs, historyReplayUserContent)
	if len(got) != 1 || got[0].Content != raw {
		t.Fatalf("history user content = %+v, want raw %q", got, raw)
	}
	if got[0].SubmitText != "" {
		t.Fatalf("provider-only wrapper should not become replay text, got %q", got[0].SubmitText)
	}
	if strings.Contains(got[0].Content, "capability-route") || strings.Contains(got[0].SubmitText, "capability-route") {
		t.Fatalf("provider-only wrapper leaked into history: %+v", got[0])
	}
}

func TestHistoryMessagesStripActiveGoalFromVisibleUserContent(t *testing.T) {
	raw := "<active-goal>\nship the approval redesign\n</active-goal>\n\ncontinue implementation"
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: raw},
		{Role: provider.RoleAssistant, Content: "done"},
	}

	got := historyMessages(msgs, control.StripComposePrefixes)
	if len(got) != 2 {
		t.Fatalf("history length = %d, want 2: %+v", len(got), got)
	}
	if got[0].Content != "continue implementation" {
		t.Fatalf("visible user content = %q, want active-goal stripped", got[0].Content)
	}
	if strings.Contains(got[0].Content, "<active-goal>") || strings.Contains(got[0].Content, "ship the approval redesign") {
		t.Fatalf("active-goal leaked into visible history content: %+v", got[0])
	}
}

func TestHistoryMessagesCarryCheckpointTurnsAcrossHiddenSyntheticUsers(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first visible"},
		{Role: provider.RoleAssistant, Content: "first answer"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "Continue pursuing the active goal. If it is complete, provide the concise final result."},
		{Role: provider.RoleAssistant, Content: "hidden continuation"},
		{Role: provider.RoleUser, Content: "second visible"},
		{Role: provider.RoleAssistant, Content: "second answer"},
	}

	got := historyMessagesFromProvider(
		msgs,
		func(content string) string { return content },
		map[int]int{1: 0, 5: 2},
	)
	var users []HistoryMessage
	for _, msg := range got {
		if msg.Role == "user" {
			users = append(users, msg)
		}
	}
	if len(users) != 2 {
		t.Fatalf("visible users = %d, want 2: %+v", len(users), got)
	}
	if users[0].CheckpointTurn == nil || *users[0].CheckpointTurn != 0 {
		t.Fatalf("first checkpoint turn = %v, want 0", users[0].CheckpointTurn)
	}
	if users[1].CheckpointTurn == nil || *users[1].CheckpointTurn != 2 {
		t.Fatalf("second checkpoint turn = %v, want 2", users[1].CheckpointTurn)
	}
}

func TestHistoryPageFromProviderMessagesWindowsVisibleUsers(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first"},
		{Role: provider.RoleAssistant, Content: "one"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "Continue pursuing the active goal. If it is complete, provide the concise final result."},
		{Role: provider.RoleAssistant, Content: "hidden continuation"},
		{Role: provider.RoleUser, Content: "second"},
		{Role: provider.RoleAssistant, Content: "two"},
		{
			Role: provider.RoleTool, Content: agent.MidTurnSteerPrefix + "\nupdate the plan",
			ToolCallID: "", Name: "", LocalOnly: true,
		},
		{Role: provider.RoleUser, Content: "third"},
		{Role: provider.RoleAssistant, Content: "three"},
	}

	latest := historyPageFromProviderMessages(
		msgs,
		func(content string) string { return content },
		map[int]int{1: 0, 5: 2, 8: 3},
		0,
		2,
	)
	if latest.StartTurn != 1 || latest.EndTurn != 3 || latest.TotalTurns != 3 || !latest.HasOlder {
		t.Fatalf("latest page metadata = %+v, want turns 1-3/3 hasOlder", latest)
	}
	if len(latest.Messages) != 5 {
		t.Fatalf("latest page length = %d, want 5: %+v", len(latest.Messages), latest.Messages)
	}
	if latest.Messages[0].Role != "user" || latest.Messages[0].Content != "second" {
		t.Fatalf("first latest message = %+v, want second user", latest.Messages[0])
	}
	if latest.Messages[0].CheckpointTurn == nil || *latest.Messages[0].CheckpointTurn != 2 {
		t.Fatalf("second user checkpoint = %v, want 2", latest.Messages[0].CheckpointTurn)
	}
	if latest.Messages[2].Role != "notice" ||
		latest.Messages[2].Code != event.NoticeCodeUnappliedSteer ||
		latest.Messages[2].Level != "warn" ||
		!strings.Contains(latest.Messages[2].Content, "not applied") ||
		!strings.Contains(latest.Messages[2].Content, "update the plan") {
		t.Fatalf("steer message = %+v, want explicit unapplied notice in second turn window", latest.Messages[2])
	}
	if latest.Messages[3].Role != "user" || latest.Messages[3].Content != "third" {
		t.Fatalf("third latest message = %+v, want third user", latest.Messages[3])
	}
	if latest.Messages[3].CheckpointTurn == nil || *latest.Messages[3].CheckpointTurn != 3 {
		t.Fatalf("third user checkpoint = %v, want 3", latest.Messages[3].CheckpointTurn)
	}

	older := historyPageFromProviderMessages(
		msgs,
		func(content string) string { return content },
		map[int]int{1: 0, 5: 2, 8: 3},
		latest.StartTurn,
		2,
	)
	if older.StartTurn != 0 || older.EndTurn != 1 || older.TotalTurns != 3 || older.HasOlder {
		t.Fatalf("older page metadata = %+v, want turns 0-1/3 no older", older)
	}
	if len(older.Messages) != 4 || older.Messages[0].Role != "system" || older.Messages[1].Content != "first" || older.Messages[3].Content != "hidden continuation" {
		t.Fatalf("older page messages = %+v, want prelude and first visible turn", older.Messages)
	}
}

func TestTranscriptCheckpointRetainsPlannerAndExecutorOutput(t *testing.T) {
	rows := roundTripDesktopTranscript(t,
		event.Event{Kind: event.Phase, Text: "planning", Source: event.UsageSourcePlanner},
		event.Event{Kind: event.Message, MessageID: "planner", Text: "planner visible plan", Reasoning: "planner thinking", Source: event.UsageSourcePlanner},
		event.Event{Kind: event.Message, MessageID: "executor", Text: "executor kept working", Source: event.UsageSourceExecutor},
	)
	if len(rows) != 3 || rows[0].Role != "phase" || rows[1].Content != "planner visible plan" || rows[1].Reasoning != "planner thinking" || rows[2].Content != "executor kept working" {
		t.Fatalf("transcript lost planner or executor output: %+v", rows)
	}
}

type cancelledDisplayRunner struct {
	executor *agent.Agent
	sink     event.Sink
	started  chan struct{}
}

type blockingPlannerProvider struct{ started chan struct{} }

func (p *blockingPlannerProvider) Name() string { return "blocking-planner" }

func (p *blockingPlannerProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (r *cancelledDisplayRunner) SetSink(sink event.Sink) { r.sink = sink }

func (r *cancelledDisplayRunner) Run(ctx context.Context, input string) error {
	session := r.executor.Session()
	session.Add(provider.Message{Role: provider.RoleUser, Content: input})
	session.Add(provider.Message{Role: provider.RoleAssistant, ReasoningContent: "checking settings\n", ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "read_file", Arguments: `{"path":"settings.json"}`,
	}}})
	session.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "call_1", Name: "read_file", Content: "partial settings"})
	r.sink.Emit(event.Event{Kind: event.Reasoning, Text: "checking settings\n", Source: event.UsageSourceExecutor})
	r.sink.Emit(event.Event{Kind: event.ToolDispatch, Source: event.UsageSourceExecutor, Tool: event.Tool{
		ID: "call_1", Name: "read_file", Args: `{"path":"settings.json"}`, ReadOnly: true,
	}})
	r.sink.Emit(event.Event{Kind: event.ToolResult, Source: event.UsageSourceExecutor, Tool: event.Tool{
		ID: "call_1", Name: "read_file", Output: "partial settings", Err: "cancelled",
	}})
	close(r.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestHistoryForTabRestoresCancelledExecutorDisplayAfterReload(t *testing.T) {
	isolateDesktopUserDirs(t)
	sess := agent.NewSession("system")
	app := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: "cancelled_tab"}
	tab := &WorkspaceTab{ID: "cancelled_tab", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	ag := agent.New(stubProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	runner := &cancelledDisplayRunner{executor: ag, started: make(chan struct{})}
	ctrl := bindHistoryRuntime(t, app, tab, runner, ag)
	t.Cleanup(ctrl.Close)
	tab.Ctrl = ctrl
	app.tabs[tab.ID] = tab

	ctrl.Send("continue setup")
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled turn did not start")
	}
	ctrl.Cancel()
	waitNotRunning(t, ctrl)
	waitForAutosaveIdle(t, tab)

	rows := currentTranscriptMessages(t, ctrl)
	var user, assistant, tool, notice bool
	for _, row := range rows {
		user = user || row.Role == "user" && row.Content == "continue setup"
		assistant = assistant || row.Role == "assistant" && row.Reasoning == "checking settings\n" && len(row.ToolCalls) == 1 && row.ToolCalls[0].ID == "call_1"
		tool = tool || row.Role == "tool" && row.ToolCallID == "call_1"
		notice = notice || row.Code == event.NoticeCodeCancelledTurn
	}
	if !user || !assistant || !tool || !notice {
		t.Fatalf("interrupted transcript lost current records: %+v", rows)
	}
}

func TestHistoryForTabRestoresPlannerDisplayWhenCancelledBeforeExecutorStarts(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: "planner_cancelled_tab"}
	tab := &WorkspaceTab{ID: "planner_cancelled_tab", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	executorSession := agent.NewSession("system")
	executor := agent.New(stubProvider{}, tool.NewRegistry(), executorSession, agent.Options{}, tab.sink)
	planner := &blockingPlannerProvider{started: make(chan struct{})}
	runner := agent.NewCoordinatorWithPlannerPolicy(planner, agent.NewSession("planner system"), nil, agent.PlannerToolRegistry(tool.NewRegistry()), agent.Options{}, executor, 0, tab.sink, nil)
	ctrl := bindHistoryRuntime(t, app, tab, runner, executor)
	defer ctrl.Close()
	ctrl.SetPlanMode(true)
	tab.Ctrl = ctrl
	app.tabs[tab.ID] = tab

	ctrl.Send("new question")
	select {
	case <-planner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("planner did not start")
	}
	ctrl.Cancel()
	waitNotRunning(t, ctrl)
	waitForAutosaveIdle(t, tab)

	canonical := ctrl.History()
	if len(canonical) != 2 || canonical[1].Role != provider.RoleUser || canonical[1].Content != "new question" {
		t.Fatalf("canonical history = %+v, want the submitted user input", canonical)
	}
	snapshot, snapshotErr := ctrl.TranscriptSnapshot(transcript.PageRequest{})
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	users := 0
	for _, record := range snapshot.Records {
		if record.Message.Role == "user" {
			users++
			if record.Message.MessageID != canonical[1].ID {
				t.Fatalf("display user ID %q differs from recovered canonical ID %q", record.Message.MessageID, canonical[1].ID)
			}
		}
	}
	if users != 1 {
		t.Fatalf("snapshot users = %d, want one identified user", users)
	}
	visible := currentTranscriptMessages(t, ctrl)
	var user, phase, notice bool
	for _, row := range visible {
		user = user || row.Role == "user" && row.Content == "new question"
		phase = phase || row.Role == "phase" && strings.Contains(row.Content, "planning")
		notice = notice || row.Code == event.NoticeCodeCancelledTurn
	}
	if !user || !phase || !notice {
		t.Fatalf("interrupted planner transcript lost current records: %+v", visible)
	}
}

func TestCancelledExecutorDisplayFollowsDetachedAndReattachedRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{tabs: map[string]*WorkspaceTab{}, detachedSessions: map[string]*WorkspaceTab{}, activeTabID: "source_tab"}
	source := &WorkspaceTab{ID: "source_tab", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	source.sink = &tabEventSink{tabID: source.ID, app: app}
	sess := agent.NewSession("system")
	executor := agent.New(stubProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	runner := &cancelledDisplayRunner{executor: executor, started: make(chan struct{})}
	ctrl := bindHistoryRuntime(t, app, source, runner, executor)
	defer ctrl.Close()
	source.Ctrl = ctrl
	app.tabs[source.ID] = source

	ctrl.Send("continue setup")
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled turn did not start")
	}
	if !app.detachRuntimeForReplacement(source) {
		t.Fatal("running session could not be detached")
	}
	path := sessionRoute(source.SessionID)
	key := sessionRuntimeKey(path)
	detached := app.detachedSessions[key]
	if detached == nil {
		t.Fatal("detached runtime missing")
	}
	target := &WorkspaceTab{ID: "reattached_tab", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	app.mu.Lock()
	delete(app.tabs, source.ID)
	app.tabs[target.ID] = target
	delete(app.detachedSessions, key)
	applyRuntimeTab(target, detached, path, app.ctx, app)
	app.activeTabID = target.ID
	app.mu.Unlock()
	runner.sink.Emit(event.Event{Kind: event.Text, MessageID: "after-reattach", Text: "after reattach", Source: event.UsageSourceExecutor})

	ctrl.Cancel()
	waitNotRunning(t, ctrl)
	waitForAutosaveIdle(t, target)
	visible := currentTranscriptMessages(t, ctrl)
	var sawBefore, sawAfter, sawNotice bool
	for _, message := range visible {
		if message.Role == "assistant" && message.Reasoning == "checking settings\n" {
			sawBefore = true
		}
		if message.Role == "assistant" && message.Content == "after reattach" {
			sawAfter = true
		}
		if message.Role == "notice" && message.Code == event.NoticeCodeCancelledTurn {
			sawNotice = true
		}
	}
	if !sawBefore || !sawAfter || !sawNotice {
		t.Fatalf("reattached cancelled history lost display state: before=%v after=%v notice=%v history=%+v", sawBefore, sawAfter, sawNotice, visible)
	}
}

func TestHistoryMessagesArchiveCompletedToolPayloads(t *testing.T) {
	largeArgs := `{"command":"` + strings.Repeat("printf x;", 300) + `"}`
	largeOutput := strings.Repeat("line of output\n", 600)
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
			ID: "call_large", Name: "bash", Arguments: largeArgs,
		}}},
		{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_large", Content: largeOutput},
	}

	got := historyMessages(msgs, func(content string) string { return content })
	if len(got) != 2 {
		t.Fatalf("history length = %d, want 2", len(got))
	}
	call := got[0].ToolCalls[0]
	if !call.ArgumentsArchived {
		t.Fatalf("tool arguments were not marked archived: %+v", call)
	}
	if call.Arguments != "" {
		t.Fatalf("archived tool arguments should be omitted from initial history, got %d bytes", len(call.Arguments))
	}
	if call.Subject == "" {
		t.Fatalf("archived tool call should keep a collapsed subject: %+v", call)
	}
	if call.Summary == "" {
		t.Fatalf("archived tool call should keep a collapsed summary: %+v", call)
	}
	result := got[1]
	if !result.ToolResultArchived {
		t.Fatalf("tool result was not marked archived: %+v", result)
	}
	if result.Content != "" {
		t.Fatalf("archived successful tool output should be omitted from initial history, got %d bytes", len(result.Content))
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), largeArgs) || strings.Contains(string(encoded), largeOutput) {
		t.Fatalf("initial history JSON still contains large args/output: %d bytes", len(encoded))
	}
}

func TestHistoryMessagesPreserveResolvedCapabilityMetadata(t *testing.T) {
	resolvedReadOnly := false
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
			ID: "call_capability", Name: "use_capability",
			Arguments:        `{"action":"call","capability_id":"mcp-tool:db/write"}`,
			ResolvedName:     "mcp__db__write",
			CapabilityID:     "mcp-tool:db/write",
			ResolvedReadOnly: &resolvedReadOnly,
		}}},
		{Role: provider.RoleTool, Name: "use_capability", ToolCallID: "call_capability", Content: "done"},
	}

	got := historyMessages(msgs, func(content string) string { return content })
	if len(got) != 2 || len(got[0].ToolCalls) != 1 {
		t.Fatalf("history = %+v", got)
	}
	call := got[0].ToolCalls[0]
	if call.ResolvedName != "mcp__db__write" || call.CapabilityID != "mcp-tool:db/write" ||
		call.ResolvedReadOnly == nil || *call.ResolvedReadOnly {
		t.Fatalf("resolved capability metadata = %+v", call)
	}
}

func TestHistoryMessagesKeepRunSkillSubjectWhenArchived(t *testing.T) {
	args := `{"name":"code-reviewer","arguments":"review this branch"}`
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
			ID: "call_skill", Name: "run_skill", Arguments: args,
		}}},
		{Role: provider.RoleTool, Name: "run_skill", ToolCallID: "call_skill", Content: "Skill completed"},
	}

	got := historyMessages(msgs, func(content string) string { return content })
	if len(got) != 2 {
		t.Fatalf("history length = %d, want 2", len(got))
	}
	call := got[0].ToolCalls[0]
	if !call.ArgumentsArchived || call.Arguments != "" {
		t.Fatalf("run_skill arguments should be archived after completion: %+v", call)
	}
	if call.Subject != "code-reviewer" {
		t.Fatalf("run_skill subject = %q, want code-reviewer", call.Subject)
	}
}

func TestHistoryMessagesKeepToolFileDiffMetadata(t *testing.T) {
	diff := "@@ -27 +27 @@\n-func save():\n+func save_file():\n"
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
			ID:        "edit",
			Name:      "edit_file",
			Arguments: `{"path":"settings/settings_IO.gd","old_string":"func save():","new_string":"func save_file():"}`,
			Diff:      diff,
			Added:     1,
			Removed:   1,
		}}},
		{Role: provider.RoleTool, Name: "edit_file", ToolCallID: "edit", Content: "edited settings/settings_IO.gd"},
	}

	got := historyMessages(msgs, func(content string) string { return content })
	call := got[0].ToolCalls[0]
	if call.Diff != diff || call.Added != 1 || call.Removed != 1 {
		t.Fatalf("history tool diff metadata = diff:%q +%d -%d", call.Diff, call.Added, call.Removed)
	}
	if !call.ArgumentsArchived || call.Arguments != "" {
		t.Fatalf("tool arguments should still be archived: %+v", call)
	}
}

func TestHistoryMessagesKeepBoundedToolErrors(t *testing.T) {
	largeError := "error: " + strings.Repeat("permission denied ", 400)
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
			ID: "call_error", Name: "bash", Arguments: `{"command":"rm protected"}`,
		}}},
		{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_error", Content: largeError},
	}

	got := historyMessages(msgs, func(content string) string { return content })
	result := got[1]
	if result.ToolResultError == "" {
		t.Fatalf("failed tool result should keep an error preview: %+v", result)
	}
	if result.Content != result.ToolResultError {
		t.Fatalf("tool result content and error preview diverged: content=%q error=%q", result.Content, result.ToolResultError)
	}
	if len(result.Content) >= len(largeError) {
		t.Fatalf("failed tool result preview was not bounded: got %d want < %d", len(result.Content), len(largeError))
	}
	if !strings.HasPrefix(result.Content, "error: permission denied") {
		t.Fatalf("failed tool result preview lost useful prefix: %q", result.Content[:min(len(result.Content), 80)])
	}
	if !result.ToolResultArchived {
		t.Fatalf("bounded failed tool result should still be marked archived for on-demand full data: %+v", result)
	}
}

func TestHistoryMessagesClipToolErrorsAtUTF8Boundary(t *testing.T) {
	largeError := "error: " + strings.Repeat("权限不足", 1000)
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
			ID: "call_unicode_error", Name: "bash", Arguments: `{"command":"rm 受保护文件"}`,
		}}},
		{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_unicode_error", Content: largeError},
	}

	got := historyMessages(msgs, func(content string) string { return content })
	result := got[1]
	if result.ToolResultError == "" {
		t.Fatalf("failed tool result should keep an error preview: %+v", result)
	}
	if !utf8.ValidString(result.ToolResultError) {
		t.Fatalf("failed tool result preview is not valid UTF-8: %q", result.ToolResultError)
	}
	if len(result.ToolResultError) >= len(largeError) {
		t.Fatalf("failed tool result preview was not bounded: got %d want < %d", len(result.ToolResultError), len(largeError))
	}
}

func TestHistoryMessagesKeepTodoWriteArguments(t *testing.T) {
	args := `{"todos":[{"content":"A","status":"in_progress"}]}`
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
			ID: "todo_1", Name: "todo_write", Arguments: args,
		}}},
		{Role: provider.RoleTool, Name: "todo_write", ToolCallID: "todo_1", Content: "Todos updated"},
	}

	got := historyMessages(msgs, func(content string) string { return content })
	call := got[0].ToolCalls[0]
	if call.ArgumentsArchived {
		t.Fatalf("todo_write arguments must remain available for restored todo panel: %+v", call)
	}
	if call.Arguments != args {
		t.Fatalf("todo_write arguments = %q, want %q", call.Arguments, args)
	}
}

func TestHistoryMessagesPreserveUnaddressableToolPayloads(t *testing.T) {
	args := `{"command":"legacy"}`
	output := "legacy output\n" + strings.Repeat("detail\n", 8)
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
			Name: "bash", Arguments: args,
		}}},
		{Role: provider.RoleTool, Name: "bash", Content: output},
	}

	got := historyMessages(msgs, func(content string) string { return content })
	call := got[0].ToolCalls[0]
	if call.ArgumentsArchived {
		t.Fatalf("tool call without an id cannot be archived for later lookup: %+v", call)
	}
	if call.Arguments != args {
		t.Fatalf("tool call without an id should keep args, got %q", call.Arguments)
	}
	result := got[1]
	if result.ToolResultArchived {
		t.Fatalf("tool result without an id cannot be archived for later lookup: %+v", result)
	}
	if result.Content != output {
		t.Fatalf("tool result without an id should keep output, got %q", result.Content)
	}
}

func TestClosingTabsPreservesCurrentSessionProfile(t *testing.T) {
	for _, keepOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("keep-only=%t", keepOnly), func(t *testing.T) {
			isolateDesktopUserDirs(t)
			app := NewApp()
			tab := &WorkspaceTab{ID: "profile", Scope: "global", WorkspaceRoot: globalTabWorkspaceRoot(), Ready: true, disabledMCP: map[string]ServerView{}}
			tab.sink = &tabEventSink{tabID: tab.ID, app: app}
			executor := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
			ctrl := bindHistoryRuntime(t, app, tab, executor, executor)
			ctrl.SetPlanMode(true)
			ctrl.SetGoal("finish the review")
			if _, _, err := ctrl.SetSessionPermissionPreset(t.Context(), control.ToolApprovalWorkspaceWrite, ctrl.PermissionSnapshot().Revision); err != nil {
				t.Fatal(err)
			}
			ref, ok := ctrl.SessionRef()
			if !ok {
				t.Fatal("missing current session identity")
			}
			service := ctrl.SessionService()
			other := &WorkspaceTab{ID: "other", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
			app.tabs[tab.ID], app.tabs[other.ID] = tab, other
			app.tabOrder, app.activeTabID = []string{tab.ID, other.ID}, tab.ID
			if keepOnly {
				if _, err := app.keepOnlyVisibleTab(other.ID); err != nil {
					t.Fatal(err)
				}
			} else if err := app.CloseTab(tab.ID); err != nil {
				t.Fatal(err)
			}
			snapshot, err := service.Query().Snapshot(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			var plan struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.Unmarshal(snapshot.Projection.PlanState, &plan); err != nil {
				t.Fatal(err)
			}
			goal, err := goaldomain.NewMachine(nil, nil).Restore(snapshot.Projection.GoalState)
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Enabled || goal == nil || goal.Objective != "finish the review" || snapshot.Projection.PermissionPreset != control.ToolApprovalWorkspaceWrite {
				t.Fatalf("closed session profile: plan=%+v goal=%+v preset=%q", plan, goal, snapshot.Projection.PermissionPreset)
			}
		})
	}
}
