package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProvisionalResumeClearsPreviousSessionJobsAndRollbackRestoresThem(t *testing.T) {
	const oldPath = "session-id:old"
	const targetPath = "session-id:target"
	client := &http.Client{}
	tab := &remoteTab{
		id: "remote-1", state: "ready", client: client, gen: 7,
		routing: remoteTabSessionRouting{currentPath: oldPath, running: map[string]bool{}},
		pendingEvents: map[string]json.RawMessage{
			"approval_request:old": json.RawMessage(`{"kind":"approval_request"}`),
		},
		runtime: remoteTabRuntimeState{
			running: true, turnStartedAt: 99, backgroundJobs: 3,
			pendingPrompt: true, cancelRequested: true, cancellable: true,
		},
	}
	a := &App{remoteTabs: map[string]*remoteTab{tab.id: tab}}
	route := a.beginRemoteTabProvisionalResume(tab.id, tab, client, tab.gen, targetPath)
	if !route.active || route.previousRuntime.backgroundJobs != 3 {
		t.Fatalf("provisional snapshot = %+v", route)
	}
	if tab.runtime.backgroundJobs != 0 || tab.runtime.running || tab.runtime.cancellable || tab.runtime.pendingPrompt || tab.runtime.cancelRequested || len(tab.pendingEvents) != 0 {
		t.Fatalf("provisional target retained previous foreground state: runtime=%+v pending=%d", tab.runtime, len(tab.pendingEvents))
	}
	if a.completeRemoteTabResumeFailure(tab.id, tab, client, tab.gen, route, "resume rejected") {
		t.Fatal("provisional route did not roll back")
	}
	if tab.runtime.backgroundJobs != 3 || !tab.runtime.running || !tab.runtime.cancellable || len(tab.pendingEvents) != 1 {
		t.Fatalf("rollback did not restore previous foreground state: runtime=%+v pending=%d", tab.runtime, len(tab.pendingEvents))
	}
}

func TestRemoteResumeReplayStopsAfterLaterSessionAdoption(t *testing.T) {
	isolateDesktopUserDirs(t)
	const oldPath = "session-id:old"
	const targetPath = "session-id:target"
	const laterPath = "session-id:later"
	client := &http.Client{}
	tab := &remoteTab{
		id: "remote-1", state: "ready", client: client, gen: 7,
		session: remoteTabSessionState{name: "target", path: targetPath},
		routing: remoteTabSessionRouting{
			currentPath: targetPath, rehydratingPath: targetPath, running: map[string]bool{},
			rehydratingFrames: []json.RawMessage{
				json.RawMessage(`{"kind":"text","text":"target-first","sessionPath":"session-id:target"}`),
				json.RawMessage(`{"kind":"text","text":"target-second","sessionPath":"session-id:target"}`),
			},
		},
	}
	log := &eventLog{}
	a := &App{remoteTabs: map[string]*remoteTab{tab.id: tab}}
	firstFrameEntered := make(chan struct{})
	releaseFirstFrame := make(chan struct{})
	var firstFrameOnce sync.Once
	a.remoteEventHook = func(name string, payload any) {
		log.add(name, payload)
		if name == "remote-tab:"+tab.id+":event" {
			data, _ := json.Marshal(payload)
			if strings.Contains(string(data), "target-first") {
				firstFrameOnce.Do(func() { close(firstFrameEntered) })
				<-releaseFirstFrame
			}
		}
	}
	route := remoteTabProvisionalResume{targetPath: targetPath, previousPath: oldPath, active: true}
	resumeDone := make(chan struct{})
	go func() {
		a.commitAndPublishRemoteTabResume(tab.id, tab, client, tab.gen, route, serveSessionEntry{Path: targetPath, Name: "target"}, "Target")
		close(resumeDone)
	}()
	select {
	case <-firstFrameEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first replay frame did not reach publication")
	}
	adoptDone := make(chan struct{})
	go func() {
		a.adoptRemoteTabFrameCurrent(tab.id, tab.gen, laterPath, true)
		close(adoptDone)
	}()
	select {
	case <-adoptDone:
		t.Fatal("later route adoption overtook an in-flight frame publication")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirstFrame)
	select {
	case <-adoptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("later route adoption did not complete after publication")
	}
	select {
	case <-resumeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("resume replay did not stop after later adoption")
	}
	events := strings.Join(log.recorded(), "\n")
	secondFrame := strings.Index(events, "target-second")
	laterReady := strings.LastIndex(events, "remote-tab:"+tab.id+":state")
	if !strings.Contains(events, "target-first") || secondFrame < 0 || laterReady < secondFrame {
		t.Fatalf("later adoption overtook the ordered replay: %s", events)
	}
	a.remoteTabMu.Lock()
	path, rehydrating := tab.routing.currentPath, tab.routing.rehydratingPath
	a.remoteTabMu.Unlock()
	if path != laterPath || rehydrating != "" {
		t.Fatalf("later route/rehydration = %q/%q, want %q/empty", path, rehydrating, laterPath)
	}
}

func TestRemoteResumeReplaysPromptArrivingDuringDrain(t *testing.T) {
	isolateDesktopUserDirs(t)
	const targetPath = "session-id:target"
	client := &http.Client{}
	tab := &remoteTab{
		id: "remote-1", state: "ready", client: client, gen: 7,
		routing: remoteTabSessionRouting{
			currentPath: targetPath, rehydratingPath: targetPath, running: map[string]bool{},
			rehydratingFrames: []json.RawMessage{
				json.RawMessage(`{"kind":"text","text":"drain-start","sessionPath":"session-id:target"}`),
			},
		},
	}
	firstPublished := make(chan struct{})
	release := make(chan struct{})
	log := &eventLog{}
	a := &App{remoteTabs: map[string]*remoteTab{tab.id: tab}}
	a.remoteEventHook = func(name string, payload any) {
		log.add(name, payload)
		data, _ := json.Marshal(payload)
		if name == "remote-tab:"+tab.id+":event" && strings.Contains(string(data), "drain-start") {
			close(firstPublished)
			<-release
		}
	}
	done := make(chan struct{})
	go func() {
		a.commitAndPublishRemoteTabResume(tab.id, tab, client, tab.gen, remoteTabProvisionalResume{targetPath: targetPath, active: true}, serveSessionEntry{Path: targetPath, Name: "target"}, "Target")
		close(done)
	}()
	select {
	case <-firstPublished:
	case <-time.After(time.Second):
		t.Fatal("resume drain did not publish its first frame")
	}
	// The aggregate snapshot has already copied an empty prompt set. A prompt
	// arriving now must therefore reach the frontend through the fenced drain.
	a.remoteTabMu.Lock()
	snapshotPending := len(tab.pendingEvents)
	a.remoteTabMu.Unlock()
	if snapshotPending != 0 {
		t.Fatalf("snapshot pending events = %d, want 0", snapshotPending)
	}
	approval := json.RawMessage(`{"kind":"approval_request","approval":{"id":"during-drain"},"sessionPath":"session-id:target","sessionCurrent":true}`)
	if !a.bufferRemoteTabResumeFrame(tab.id, tab.gen, targetPath, "approval_request", approval) {
		t.Fatal("prompt arriving during drain was not buffered")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("resume drain did not finish")
	}
	events := strings.Join(log.recorded(), "\n")
	if !strings.Contains(events, `"id":"during-drain"`) {
		t.Fatalf("prompt arriving after snapshot was not replayed: %s", events)
	}
	a.remoteTabMu.Lock()
	pending, rehydrating := len(tab.pendingEvents), tab.routing.rehydratingPath
	a.remoteTabMu.Unlock()
	if pending != 1 || rehydrating != "" {
		t.Fatalf("post-drain pending/rehydrating = %d/%q, want 1/empty", pending, rehydrating)
	}
}
