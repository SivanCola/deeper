package control

import (
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/event"
	"deepzora/internal/guardian"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func TestCurrentSessionRotationResetsGuardianContext(t *testing.T) {
	for _, clear := range []bool{false, true} {
		name := "new"
		if clear {
			name = "clear"
		}
		t.Run(name, func(t *testing.T) {
			allow := testutil.Turn{Text: `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"allowed"}`}
			prov := testutil.NewMock("guardian", allow, allow)
			gs := guardian.NewSession(prov, tool.NewRegistry(), guardian.PolicyPrompt(), "guardian", 0, nil, event.Discard)
			exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
			c := newCurrentOwnedTestController(t, Options{Executor: exec, Guardian: gs})
			old := agent.NewSession("system")
			old.Add(provider.Message{Role: provider.RoleUser, Content: "old session private context"})
			if allowed, _, err := gs.ReviewVerdict(t.Context(), "read_file", nil, old); err != nil || !allowed {
				t.Fatalf("initial review: %v, %v", allowed, err)
			}
			var err error
			if clear {
				err = c.ClearSession()
			} else {
				err = c.NewSession()
			}
			if err != nil {
				t.Fatal(err)
			}
			fresh := agent.NewSession("system")
			fresh.Add(provider.Message{Role: provider.RoleUser, Content: "new session task"})
			if allowed, _, err := gs.ReviewVerdict(t.Context(), "read_file", nil, fresh); err != nil || !allowed {
				t.Fatalf("fresh review: %v, %v", allowed, err)
			}
			request := prov.LastRequest()
			if request == nil || len(request.Messages) != 2 {
				t.Fatalf("fresh review retained old conversation: %+v", request)
			}
			text := requestMessagesText(request.Messages)
			if strings.Contains(text, "old session private context") || !strings.Contains(text, "new session task") {
				t.Fatalf("review context crossed current identities: %s", text)
			}
		})
	}
}
