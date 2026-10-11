package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/jobs"
	"deepzora/internal/provider"
)

func chatTUIWithRunningBackgroundJob(t *testing.T) chatTUI {
	t.Helper()
	manager := jobs.NewManager(event.Discard)
	ctrl := newOwnedTestController(t, control.Options{Jobs: manager})
	t.Cleanup(ctrl.Close)
	manager.Start("task", "running", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	m := newTestChatTUI()
	m.ctrl = ctrl
	m.modelRef = "deepseek-flash/deepseek-flash"
	m.buildController = func(controllerBuildSpec, []provider.Message, string, control.SessionAPI) (*control.Controller, error) {
		t.Fatal("runtime switch built a replacement while a background job was running")
		return nil, nil
	}
	return m
}

func TestRuntimeSwitchesRejectRunningBackgroundJobs(t *testing.T) {
	t.Run("model", func(t *testing.T) {
		m := chatTUIWithRunningBackgroundJob(t)
		m.runModelSubcommand("/model deepseek-chat/deepseek-chat")
		if m.pendingModelSwitch != nil {
			t.Fatal("model switch queued a rebuild while a background job was running")
		}
	})

	t.Run("effort", func(t *testing.T) {
		isolateUserConfig(t)
		m := chatTUIWithRunningBackgroundJob(t)
		if cmd := m.runEffortCommand("/effort max"); cmd != nil {
			t.Fatal("effort switch queued a rebuild while a background job was running")
		}
	})

	t.Run("skill refresh", func(t *testing.T) {
		m := chatTUIWithRunningBackgroundJob(t)
		if m.scheduleSkillSessionRefresh("skill refresh", "") {
			t.Fatal("skill refresh queued a rebuild while a background job was running")
		}
	})

	t.Run("language", func(t *testing.T) {
		isolateUserConfig(t)
		m := chatTUIWithRunningBackgroundJob(t)
		if cmd := m.runLanguageSubcommand("/language zh"); cmd != nil {
			t.Fatal("language switch queued a rebuild while a background job was running")
		}
		if _, err := os.Stat(config.UserConfigPath()); !os.IsNotExist(err) {
			t.Fatalf("blocked language switch wrote config, stat err=%v", err)
		}
	})

	t.Run("currency", func(t *testing.T) {
		isolateUserConfig(t)
		m := chatTUIWithRunningBackgroundJob(t)
		if cmd := m.runCurrencySubcommand("/currency CNY"); cmd != nil {
			t.Fatal("currency switch queued a rebuild while a background job was running")
		}
		if _, err := os.Stat(config.UserConfigPath()); !os.IsNotExist(err) {
			t.Fatalf("blocked currency switch wrote config, stat err=%v", err)
		}
	})
}

// divergedSessionController builds a controller whose in-memory transcript has
// diverged from what path holds on disk, so its next Snapshot hits a conflict
// and retargets the controller to a recovery branch.
func divergedSessionController(t *testing.T, dir, path string) *control.Controller {
	return divergedSessionControllerWithRecovery(t, dir, path, nil)
}

func divergedSessionControllerWithRecovery(t *testing.T, dir, path string, onRecovered func(control.SessionRecoveryInfo) error) *control.Controller {
	t.Helper()
	disk := agent.NewSession("sys prompt")
	disk.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	disk.Add(provider.Message{Role: provider.RoleAssistant, Content: "disk"})
	if err := disk.Save(path); err != nil {
		t.Fatal(err)
	}
	stale := agent.NewSession("sys prompt")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "local"})
	return newOwnedTestController(t, control.Options{
		Executor:           agent.New(nil, nil, stale, agent.Options{}, event.Discard),
		SessionDir:         dir,
		SessionPath:        path,
		Label:              "deepseek-flash",
		OnSessionRecovered: onRecovered,
	})
}

func TestCompactDoneDoesNotRepeatMaintenanceSnapshot(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "compact-active-conflict.jsonl")

	m := newTestChatTUI()
	m.ctrl = divergedSessionController(t, dir, active)
	m.leases = control.NewSessionLeaseKeeper()
	t.Cleanup(m.leases.Release)
	if err := m.leases.Rebind(active); err != nil {
		t.Fatalf("seed active lease: %v", err)
	}

	next, _ := m.Update(compactDoneMsg{})
	m = next.(chatTUI)

	if got := m.ctrl.SessionPath(); got != active {
		t.Fatalf("compact completion repeated snapshot and moved session path to %q, want %q", got, active)
	}
	if got, want := m.leases.HeldPath(), agent.CanonicalSessionPath(active); got != want {
		t.Fatalf("lease after compact completion = %q, want unchanged %q", got, want)
	}
}

// TestAdoptCarriedHistoryRefreshesLeadingSystemPrompt pins the fix for the
// bug where /model, /effort, /work-mode, and skill-toggle rebuilds carried
// the outgoing profile's system prompt forward: the freshly built controller
// already has its own leading system message for the target profile, but
// AdoptHistory replaces the whole history (including that message) with the
// carried one unless the caller splices it in first.
func TestAdoptCarriedHistoryRefreshesLeadingSystemPrompt(t *testing.T) {
	fresh := newOwnedTestController(t, control.Options{
		Executor: agent.New(nil, nil, agent.NewSession("system prompt for profile delivery"), agent.Options{}, event.Discard),
	})
	carry := []provider.Message{
		{Role: provider.RoleSystem, Content: "system prompt for profile balanced"},
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "hi"},
	}

	if err := adoptCarriedHistoryPreservingProfileAndGrants(fresh, carry, "", nil); err != nil {
		t.Fatalf("adoptCarriedHistoryPreservingProfileAndGrants: %v", err)
	}

	history := fresh.History()
	if len(history) != 3 || history[0].Role != provider.RoleSystem {
		t.Fatalf("history = %+v, want 3 messages with a leading system message", history)
	}
	if got, want := history[0].Content, "system prompt for profile delivery"; got != want {
		t.Fatalf("leading system message = %q, want %q (stale outgoing profile carried forward)", got, want)
	}
	if history[1].Content != "hello" || history[2].Content != "hi" {
		t.Fatalf("history = %+v, want carried user/assistant turns preserved", history)
	}
}

// TestAdoptCarriedHistoryRestoresSessionAuthorizations pins the fix for a
// rebuild dropping same-session "Allow for this session" tool grants and
// Plan-mode read-only command trust, forcing the user to re-approve
// something already granted this session after every /model, /effort, or
// /work-mode switch.
func TestAdoptCarriedHistoryRestoresSessionAuthorizations(t *testing.T) {
	old := newOwnedTestController(t, control.Options{})
	old.RestoreSessionAuthorizations(control.SessionAuthorizations{
		Grants: []string{"bash|go test ./..."},
	})

	fresh := newOwnedTestController(t, control.Options{
		Executor: agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard),
	})

	if err := adoptCarriedHistoryPreservingProfileAndGrants(fresh, nil, "", old); err != nil {
		t.Fatalf("adoptCarriedHistoryPreservingProfileAndGrants: %v", err)
	}

	got := fresh.SessionAuthorizations()
	if len(got.Grants) != 1 || got.Grants[0] != "bash|go test ./..." {
		t.Fatalf("restored grants = %+v, want [\"bash|go test ./...\"]", got.Grants)
	}

}

// TestAdoptCarriedHistoryPersistsRefreshedSystemPromptToDisk pins the disk
// half of the splice in adoptCarriedHistoryPreservingProfileAndGrants: the
// refreshed leading system message must be persisted at switch time, because
// nothing saves again until the next turn ends — quitting right after a
// /model, /effort, or /work-mode switch and resuming would otherwise revive
// the outgoing profile's contract from disk.
func TestAdoptCarriedHistoryPersistsRefreshedSystemPromptToDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adopt-persist.jsonl")

	oldSession := agent.NewSession("system prompt for profile balanced")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldSession.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi"})
	if err := oldSession.Save(path); err != nil {
		t.Fatalf("save base session: %v", err)
	}

	fresh := newOwnedTestController(t, control.Options{
		Executor:   agent.New(nil, nil, agent.NewSession("system prompt for profile delivery"), agent.Options{}, event.Discard),
		SessionDir: dir,
	})
	carry := []provider.Message{
		{Role: provider.RoleSystem, Content: "system prompt for profile balanced"},
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "hi"},
	}

	if err := adoptCarriedHistoryPreservingProfileAndGrants(fresh, carry, path, nil); err != nil {
		t.Fatalf("adoptCarriedHistoryPreservingProfileAndGrants: %v", err)
	}

	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("load transcript after adopt: %v", err)
	}
	msgs := loaded.Snapshot()
	if len(msgs) != 3 || msgs[0].Role != provider.RoleSystem {
		t.Fatalf("on-disk history after adopt = %+v, want 3 messages with a leading system message", msgs)
	}
	if got, want := msgs[0].Content, "system prompt for profile delivery"; got != want {
		t.Fatalf("on-disk leading system message = %q, want %q (quit + resume would revive the outgoing contract)", got, want)
	}
}

func TestAdoptCarriedHistoryReportsSnapshotFailure(t *testing.T) {
	invalidPath := filepath.Join(t.TempDir(), "transcript-is-a-directory")
	if err := os.Mkdir(invalidPath, 0o755); err != nil {
		t.Fatalf("mkdir invalid transcript path: %v", err)
	}
	fresh := newOwnedTestController(t, control.Options{
		Executor: agent.New(nil, nil, agent.NewSession("system prompt for profile delivery"), agent.Options{}, event.Discard),
	})
	carry := []provider.Message{
		{Role: provider.RoleSystem, Content: "system prompt for profile balanced"},
		{Role: provider.RoleUser, Content: "hello"},
	}

	err := adoptCarriedHistoryPreservingProfileAndGrants(fresh, carry, invalidPath, nil)
	if err == nil || !strings.Contains(err.Error(), "snapshot after runtime switch") {
		t.Fatalf("adopt error = %v, want snapshot failure", err)
	}
}
