package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/session"
)

func currentInboxController(t *testing.T) (*Controller, *session.Service) {
	t.Helper()
	executor := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	return bindCurrentBranchController(t, Options{Executor: executor, SystemPrompt: "system", Sink: event.Discard})
}

func TestCurrentInboxSurvivesColdOpenAndStaysWithItsIdentity(t *testing.T) {
	root := t.TempDir()
	newController := func() (*Controller, *session.Service) {
		t.Helper()
		service, err := session.NewService("test", session.NewFilesystemPersistence(root))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
		executor := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
		return newOwnedTestController(t, Options{Executor: executor, SessionService: service, ExclusiveSession: true}), service
	}
	ctrl, service := newController()
	ref, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{SessionID: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrl.SetInboxPaused(true); err != nil {
		t.Fatal(err)
	}
	receipt, err := ctrl.EnqueueInbox(InboxRequest{Submit: "preserve pending work", Idempotency: "request"})
	if err != nil {
		t.Fatal(err)
	}
	ctrl.Close()
	<-ctrl.Closed()
	if err := service.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, _ := newController()
	if _, err := reopened.OpenSession(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	got, found, err := reopened.LookupInboxReceipt("request")
	if err != nil || !found || got.ItemID != receipt.ItemID {
		t.Fatalf("cold receipt = %+v, found=%v, err=%v", got, found, err)
	}
	if _, env, err := reopened.ReadInboxItem(receipt.ItemID); err != nil || env.SubmitText != "preserve pending work" {
		t.Fatalf("cold pending body = %+v, %v", env, err)
	}
	if snap := reopened.InboxSnapshot(); !snap.Paused || len(snap.Items) != 1 {
		t.Fatalf("cold inbox = %+v", snap)
	}
	if err := reopened.NewSession(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := reopened.LookupInboxReceipt("request"); err != nil || found {
		t.Fatalf("receipt leaked to new identity: found=%v err=%v", found, err)
	}
	if _, err := reopened.OpenSession(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if got, found, err := reopened.LookupInboxReceipt("request"); err != nil || !found || got.ItemID != receipt.ItemID {
		t.Fatalf("return lost receipt = %+v, found=%v err=%v", got, found, err)
	}
	if _, err := os.Stat(filepath.Join(root, ref.SessionID, "inbox", "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ref.SessionID+".inbox")); !os.IsNotExist(err) {
		t.Fatalf("legacy sibling inbox created: %v", err)
	}
}
