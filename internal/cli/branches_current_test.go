package cli

import (
	"context"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func newCurrentBranchChatTUI(t *testing.T, scope func(context.Context) (map[string]bool, error)) (chatTUI, *control.Controller, *session.Service) {
	t.Helper()
	prov := testutil.NewMock("branch", testutil.Turn{Text: "root answer"})
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	ctrl := newCurrentTestController(t, control.Options{Runner: exec, Executor: exec, SystemPrompt: "sys", Label: "test", Sink: event.Discard, SessionReadScope: scope})
	if _, err := ctrl.BindFreshSession(t.Context(), "root-current"); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RunTurn(t.Context(), "root prompt"); err != nil {
		t.Fatal(err)
	}
	return newChatTUI(ctrl, "", make(chan event.Event, 1), 80), ctrl, ctrl.SessionService()
}

func TestBranchAndSwitchCommandsUseIndependentSessions(t *testing.T) {
	m, ctrl, service := newCurrentBranchChatTUI(t, nil)
	parent, _ := ctrl.SessionRef()
	m.runBranchCommand("/branch experiment")
	child, _ := ctrl.SessionRef()
	if child == parent {
		t.Fatalf("/branch failed to create a current child: %+v", child)
	}
	tree := strings.Join(*m.pendingCommit, "\n")
	if !strings.Contains(tree, "experiment") || !strings.Contains(tree, "current") {
		t.Fatalf("/branch tree must list the child session as current:\n%s", tree)
	}
	m.runSwitchCommand("/switch " + parent.SessionID)
	if got, _ := ctrl.SessionRef(); got != parent || !m.sessionSwitch {
		t.Fatalf("/switch to parent: identity %+v, switch %v", got, m.sessionSwitch)
	}
	m.sessionSwitch = false
	m.runSwitchCommand("/switch experiment")
	if got, _ := ctrl.SessionRef(); got != child || !m.sessionSwitch {
		t.Fatalf("/switch by name: identity %+v, switch %v", got, m.sessionSwitch)
	}
	for _, ref := range []session.SessionRef{parent, child} {
		messages, err := service.Query().History(t.Context(), ref)
		if err != nil || len(messages) != 3 || messages[1].Content != "root prompt" || messages[2].Content != "root answer" {
			t.Fatalf("independent history %+v: %+v, %v", ref, messages, err)
		}
	}
}
