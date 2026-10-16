package main

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/control"
)

func awaitRemovalTest[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent removal operation did not complete")
		var zero T
		return zero
	}
}

func TestTopicRemovalJoinsAutosaveOutsideTitleLock(t *testing.T) {
	for _, priorSave := range []bool{false, true} {
		t.Run(fmt.Sprintf("prior-save=%t", priorSave), func(t *testing.T) {
			a, _, rt, _, _ := newCanonicalTitleFixture(t)
			installNoopRuntimeEvents(a)
			tab := a.tabs["test"]
			if priorSave {
				a.scheduleTabSnapshot(tab.ID)
				waitForAutosaveIdle(t, tab)
			}
			req := inspectedTopicRemoval(t, a, tab.TopicID)
			titleReached := make(chan struct{})
			a.lifecycleCheckpointHook = func(phase string) {
				switch phase {
				case "before-archive-commit":
					a.scheduleTabSnapshot(tab.ID)
					awaitRemovalTest(t, titleReached)
				case "before-autosave-publish":
					close(titleReached)
				}
			}
			done := make(chan error, 1)
			go func() {
				out, err := a.RemoveTopic(req)
				if err == nil && !out.Committed {
					err = fmt.Errorf("removal: %+v", out)
				}
				done <- err
			}()
			if err := awaitRemovalTest(t, done); err != nil {
				t.Fatal(err)
			}
			tab.saveMu.Lock()
			stopped := tab.closing && !tab.saving && tab.saveCond != nil
			tab.saveMu.Unlock()
			if !stopped {
				t.Fatal("removal did not join its autosave")
			}
			a.lifecycleCheckpointHook = nil
			state, err := a.workspaceRegistry().Load(t.Context())
			if err != nil || state.SessionStates[rt.Ref().SessionID].Lifecycle != workspacestate.Archived {
				t.Fatalf("archive state: %+v %v", state, err)
			}
		})
	}
}

func TestColdSessionRemovalReleasesTitleLocksBeforeCleanup(t *testing.T) {
	a, topic, _ := topicRemovalFixture(t, "global", "Placeholder")
	ref, _ := currentTopicInfo(t, a, topic.ID)
	tab := &WorkspaceTab{ID: "failed", TopicID: topic.ID, SessionID: ref.SessionID, Scope: "global", StartupErr: "unavailable", Ready: true}
	a.tabs[tab.ID] = tab
	a.tabOrder = []string{tab.ID}
	a.activeTabID = tab.ID
	checked := false
	a.lifecycleCheckpointHook = func(phase string) {
		if phase != "before-topic-runtime-cleanup" {
			return
		}
		checked = true
		if !a.topicTitleMutationMu.TryLock() {
			t.Error("cleanup holds title lock")
		} else {
			a.topicTitleMutationMu.Unlock()
		}
		if !a.mu.TryLock() {
			t.Error("cleanup holds App lock")
		} else {
			a.mu.Unlock()
		}
		if a.sessionRemovalMu.TryLock() {
			a.sessionRemovalMu.Unlock()
			t.Error("cleanup lost removal ownership")
		}
	}
	out, err := a.RemoveTopic(inspectedTopicRemoval(t, a, topic.ID))
	if err != nil || !out.Committed || !checked {
		t.Fatalf("removal: %+v %v cleanup=%t", out, err, checked)
	}
}

type removalBlockingSnapshot struct {
	control.SessionAPI
	started chan struct{}
	resume  chan struct{}
}

func (c *removalBlockingSnapshot) Snapshot() error {
	close(c.started)
	<-c.resume
	return c.SessionAPI.Snapshot()
}

func TestQuiesceWaitsForFirstAutosave(t *testing.T) {
	isolateDesktopUserDirs(t)
	a, tab := appWithTab(t)
	c := &removalBlockingSnapshot{SessionAPI: tab.Ctrl, started: make(chan struct{}), resume: make(chan struct{})}
	tab.Ctrl = c
	var release sync.Once
	defer release.Do(func() { close(c.resume) })
	a.scheduleTabSnapshot(tab.ID)
	awaitRemovalTest(t, c.started)
	tab.saveMu.Lock()
	initialized := tab.saving && tab.saveCond != nil
	tab.saveMu.Unlock()
	if !initialized {
		t.Fatal("first in-flight save has no completion condition")
	}
	done := make(chan struct{})
	go func() { a.quiesceTabAutosave(tab); close(done) }()
	// Acquiring saveMu after closing is set proves the join has reached its
	// condition wait while the actual Snapshot is still blocked.
	deadline := time.Now().Add(10 * time.Second)
	for {
		tab.saveMu.Lock()
		closing := tab.closing
		tab.saveMu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("quiesce did not stop save admission")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("quiesce returned before the first write finished")
	default:
	}
	release.Do(func() { close(c.resume) })
	awaitRemovalTest(t, done)
	if tab.saving {
		t.Fatal("save still running after join")
	}
}
