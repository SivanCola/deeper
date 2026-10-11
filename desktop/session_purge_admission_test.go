package main

import (
	"encoding/json"
	"errors"
	"testing"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

func TestPurgeCommandUnrelatedSessionMustNotBlockPurge(t *testing.T) {
	a, victim := lifecycleFixture(t)
	other := addLifecycleFixtureSession(t, a, "review-other")
	if err := a.ArchiveCanonicalSession(victim); err != nil {
		t.Fatal(err)
	}
	req := lifecycleRequest(t, a, victim, "review-unrelated", "purge")
	if err := a.workspaceRegistry().ArchiveSession(t.Context(), other.SessionID); err != nil {
		t.Fatal(err)
	}
	result, err := a.ApplySessionLifecycle(req)
	if err != nil || !result.Committed {
		t.Fatalf("unrelated session blocked purge: result=%+v err=%v", result, err)
	}
}

func TestPurgeCommandSnapshotConflictIsTerminal(t *testing.T) {
	a, ref := lifecycleFixture(t)
	if err := a.ArchiveCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	req := lifecycleRequest(t, a, ref, "old-confirmation", "purge")
	store := a.workspaceRegistry()
	if err := store.RestoreSession(t.Context(), ref.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveSession(t.Context(), ref.SessionID); err != nil {
		t.Fatal(err)
	}
	first, err := a.ApplySessionLifecycle(req)
	if err != nil || len(first.Items) != 1 || first.Items[0].ErrorCode != "state_conflict" || first.Items[0].Retryable {
		t.Fatalf("result=%+v err=%v", first, err)
	}
	second, err := a.ApplySessionLifecycle(req)
	b1, _ := json.Marshal(first)
	b2, _ := json.Marshal(second)
	if err != nil || string(b1) != string(b2) {
		t.Fatalf("terminal result changed: %s %s %v", b1, b2, err)
	}
	if history, err := a.desktopSessionService("").Query().History(t.Context(), ref); err != nil || len(history) == 0 {
		t.Fatalf("lost history: %v", err)
	}
	for _, change := range []func(*SessionLifecycleRequest){
		func(r *SessionLifecycleRequest) { r.ExpectedGeneration++ },
		func(r *SessionLifecycleRequest) { r.Action = "restore" },
		func(r *SessionLifecycleRequest) {
			r.Targets = []SessionLifecycleTarget{{Ref: &session.SessionRef{HostID: localDesktopHostID, SessionID: "other"}}}
		},
	} {
		changed := req
		change(&changed)
		if _, err := a.ApplySessionLifecycle(changed); !errors.Is(err, workspacestate.ErrMutationConflict) {
			t.Fatalf("changed request accepted: %v", err)
		}
	}
}

func TestPurgeCommandFirstDeliverySeparatesChangedTargets(t *testing.T) {
	a, unchanged := lifecycleFixture(t)
	changed := addLifecycleFixtureSession(t, a, "changed-target")
	for _, ref := range []session.SessionRef{unchanged, changed} {
		if err := a.ArchiveCanonicalSession(ref); err != nil {
			t.Fatal(err)
		}
	}
	req := lifecycleRequest(t, a, unchanged, "mixed-first-delivery", "purge")
	req.Targets = append(req.Targets, SessionLifecycleTarget{Ref: &changed})
	if err := a.workspaceRegistry().RestoreSession(t.Context(), changed.SessionID); err != nil {
		t.Fatal(err)
	}
	result, err := a.ApplySessionLifecycle(req)
	if err != nil || len(result.Items) != 2 || !result.Items[0].Committed || result.Items[1].ErrorCode != "state_conflict" || result.Items[1].Retryable {
		t.Fatalf("mixed admission result=%+v err=%v", result, err)
	}
	if history, err := a.desktopSessionService("").Query().History(t.Context(), changed); err != nil || len(history) == 0 {
		t.Fatalf("changed target lost body: %v", err)
	}
}

func TestPurgeCommandRestoreWhileWaitingForRuntimeLock(t *testing.T) {
	a, ref := lifecycleFixture(t)
	if err := a.ArchiveCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	req := lifecycleRequest(t, a, ref, "restore-before-runtime-lock", "purge")
	a.runtimeMutationBeforeLockHook = func(operation string) {
		if operation == "purge lifecycle command" {
			if err := a.workspaceRegistry().RestoreSession(t.Context(), ref.SessionID); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := a.ApplySessionLifecycle(req)
	if err != nil || len(result.Items) != 1 || result.Items[0].ErrorCode != "state_conflict" || result.Items[0].Retryable {
		t.Fatalf("late conflict=%+v err=%v", result, err)
	}
	if history, err := a.desktopSessionService("").Query().History(t.Context(), ref); err != nil || len(history) == 0 {
		t.Fatalf("restore lost body: %v", err)
	}
}
