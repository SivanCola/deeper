package main

import (
	"context"
	"path/filepath"
	"testing"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
	"deepzora/internal/transcript"
)

func TestTranscriptFollowFenceSurvivesAgentPresetReplacement(t *testing.T) {
	a := newManualSessionTestApp(t)
	tab, _ := manualAgentPresetTab(t, a, "default")
	old := a.controllerForTab(tab).(*control.Controller)
	_, owner, _ := old.SessionBinding()
	if _, err := a.SaveAgentPreset(tab.ID, "user", "review", "---\ntools: []\n---\nReview changes."); err != nil {
		t.Fatal(err)
	}
	api, current, err := a.transcriptAPIForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := api.(control.TranscriptFollowAPI).TranscriptFollow(t.Context(), transcript.FollowRequest{})
	if err != nil || initial.Snapshot == nil {
		t.Fatalf("initial follow = %+v, %v", initial, err)
	}
	defer owner.FollowTranscript(context.Background(), transcript.FollowRequest{Subscription: initial.Subscription, Close: true})
	// Pause at the RPC's post-read fence so publication and old-controller
	// teardown deterministically precede validation of the completed baseline.
	if err := a.SetAgentPresetForTab(tab.ID, "review"); err != nil {
		t.Fatal(err)
	}
	next := a.controllerForTab(tab).(*control.Controller)
	_, nextOwner, _ := next.SessionBinding()
	if next == old || nextOwner != owner {
		t.Fatal("preset replacement did not retain the exact transcript runtime")
	}
	if _, retiredOwner, _ := old.SessionBinding(); retiredOwner != nil {
		t.Fatal("outgoing controller was not retired before the follow fence")
	}
	if !current() {
		t.Fatal("same-runtime preset replacement invalidated a completed transcript baseline")
	}
	// A read may start after the same controller has released its binding.
	// The captured reader must still use the runtime that owns the transcript.
	baseline, err := api.(control.TranscriptFollowAPI).TranscriptFollow(t.Context(), transcript.FollowRequest{})
	if err != nil || baseline.Snapshot == nil {
		t.Fatalf("follow after controller retirement = %+v, %v", baseline, err)
	}
	defer owner.FollowTranscript(context.Background(), transcript.FollowRequest{Subscription: baseline.Subscription, Close: true})
	if _, err := api.TranscriptSnapshot(transcript.PageRequest{}); err != nil {
		t.Fatalf("snapshot after controller retirement: %v", err)
	}
	if _, err := api.(control.TranscriptOutlineAPI).TranscriptOutline(transcript.OutlineRequest{SnapshotID: initial.Snapshot.SnapshotID}); err != nil {
		t.Fatalf("outline after controller retirement: %v", err)
	}
	changes, err := a.TranscriptFollowForTab(tab.ID, transcript.FollowRequest{
		Subscription: initial.Subscription, AfterRevision: initial.Snapshot.ProjectionRevision,
	})
	if err != nil || changes.ResetRequired || len(changes.Changes) == 0 {
		t.Fatalf("retired controller lost the runtime's subscription: %+v, %v", changes, err)
	}
}

func newTranscriptFenceController(t *testing.T, id string) *control.Controller {
	t.Helper()
	service, err := session.NewService("local", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := control.New(control.Options{SessionService: service, SessionRuntime: runtime, ExclusiveSession: true, Sink: event.Discard})
	t.Cleanup(ctrl.Close)
	return ctrl
}

func TestTranscriptFollowFenceRejectsReplacedOwners(t *testing.T) {
	for _, name := range []string{"removed tab", "replacement tab", "runtime epoch", "same-session runtime", "different session", "released binding"} {
		t.Run(name, func(t *testing.T) {
			ctrl := newTranscriptFenceController(t, "original")
			tab := &WorkspaceTab{ID: "tab", Ctrl: ctrl, Ready: true}
			a := NewApp()
			a.tabs[tab.ID] = tab
			a.mu.Lock()
			rt := a.newSessionRuntimeLocked(tab, "")
			a.mu.Unlock()
			_, current, err := a.transcriptAPIForTab(tab.ID)
			if err != nil || !current() {
				t.Fatalf("initial fence = %v", err)
			}
			a.mu.Lock()
			switch name {
			case "removed tab":
				delete(a.tabs, tab.ID)
			case "replacement tab":
				a.tabs[tab.ID] = &WorkspaceTab{ID: tab.ID, Ctrl: ctrl, Ready: true}
			case "runtime epoch":
				rt.Epoch = "replacement-epoch"
			case "same-session runtime":
				tab.Ctrl = newTranscriptFenceController(t, "original")
			case "different session":
				tab.Ctrl = newTranscriptFenceController(t, "another")
			case "released binding":
				if err := ctrl.ReleaseSessionRuntimeBinding(); err != nil {
					t.Fatal(err)
				}
			}
			a.mu.Unlock()
			if current() {
				t.Fatal("replaced transcript owner passed the response fence")
			}
		})
	}
}

func TestTranscriptFollowFenceRetainsLegacyControllerIdentity(t *testing.T) {
	ctrl := control.New(control.Options{Sink: event.Discard})
	defer ctrl.Close()
	tab := &WorkspaceTab{ID: "tab", Ctrl: ctrl, Ready: true}
	a := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}}
	_, current, err := a.transcriptAPIForTab(tab.ID)
	if err != nil || !current() {
		t.Fatalf("initial legacy fence = %v", err)
	}
	next := control.New(control.Options{Sink: event.Discard})
	defer next.Close()
	a.mu.Lock()
	tab.Ctrl = next
	a.mu.Unlock()
	if current() {
		t.Fatal("legacy controller replacement passed the response fence")
	}
}
