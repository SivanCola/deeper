package cli

import (
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
)

func TestWebSlashResumesCurrentIdentity(t *testing.T) {
	for _, materialized := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "conversation"}[materialized], func(t *testing.T) {
			executor := agent.New(testutil.NewMock("test", testutil.Turn{Text: "reply"}), nil, agent.NewSession("system"), agent.Options{}, event.Discard)
			ctrl := newCurrentTestController(t, control.Options{Executor: executor, Runner: executor})
			ref, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if materialized {
				if err := ctrl.RunTurn(t.Context(), "hello"); err != nil {
					t.Fatal(err)
				}
			}
			before := ctrl.History()
			m := newTestChatTUI()
			m.ctrl, m.modelRef = ctrl, "test/model"
			cmd := m.runSlashCommand("/web")
			if cmd == nil || !m.launchWebOnExit || m.launchWebSessionID != ref.SessionID || m.launchWebModelRef != m.modelRef {
				t.Fatal("handoff lost the current identity or model")
			}
			if _, ok := cmd().(tuiShutdownMsg); !ok {
				t.Fatal("handoff did not request TUI shutdown")
			}
			args := strings.Join(webHandoffArgs(m.launchWebSessionID, m.launchWebModelRef), " ")
			if args != "--resume "+ref.SessionID+" --model test/model" {
				t.Fatalf("handoff arguments=%q", args)
			}
			service := ctrl.SessionService()
			ctrl.Close()
			<-ctrl.Closed()
			next := newOwnedTestController(t, control.Options{Executor: agent.New(nil, nil, agent.NewSession("other startup"), agent.Options{}, event.Discard), SessionService: service, ExclusiveSession: true})
			if err := prepareServeSession(next, cliResumeTarget{ref: ref, service: service}, ""); err != nil {
				t.Fatal(err)
			}
			if got, ok := next.SessionRef(); !ok || got != ref || !reflect.DeepEqual(next.History(), before) {
				t.Fatal("Web preparation did not preserve identity and persisted history")
			}
		})
	}
}

func TestWebSlashKeepsTUIWhenCurrentSessionCannotBeCreated(t *testing.T) {
	m := newTestChatTUI()
	m.ctrl = newOwnedTestController(t, control.Options{SessionDir: t.TempDir()})
	if cmd := m.runSlashCommand("/web"); cmd != nil || m.launchWebOnExit {
		t.Fatal("failed session creation started a Web handoff")
	}
}
