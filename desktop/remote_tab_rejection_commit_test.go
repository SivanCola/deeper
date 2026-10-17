package main

import (
	"strings"
	"testing"
	"time"
)

func TestRemoteResumeFailurePublishesRestoredIdentity(t *testing.T) {
	const oldPath = "session-id:s1"
	const targetPath = "session-id:s2"
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{
		{SessionID: remoteSessionIDFromRoute(oldPath), Name: "s1", Path: oldPath, Title: "First", Current: true},
		{SessionID: remoteSessionIDFromRoute(targetPath), Name: "s2", Path: targetPath, Title: "Second"},
	})
	kernel := &fakeRemoteKernel{
		statuses:    []RemoteConnectionStatusView{{HostID: "box", State: "connected"}},
		ensureView:  RemoteServerView{HostID: "box", State: "ready", LocalURL: fs.server.URL},
		ensureToken: "s3cret",
	}
	seedBridgeTestHost(t, "box")
	a := &App{remoteRuntime: kernel}
	cleanupRemoteTabPumps(t, a)
	type identity struct{ name, path, route, title string }
	observed := make(chan identity, 1)
	a.remoteEventHook = func(name string, payload any) {
		state, ok := payload.(RemoteTabStateView)
		if !ok || !strings.Contains(state.Error, "already leased") {
			return
		}
		tabID := strings.TrimSuffix(strings.TrimPrefix(name, "remote-tab:"), ":state")
		a.remoteTabMu.Lock()
		tab := a.remoteTabs[tabID]
		got := identity{tab.session.name, remoteSessionIdentityRoute(tab.session.path, tab.session.sessionID), tab.routing.currentPath, tab.topicTitle}
		a.remoteTabMu.Unlock()
		observed <- got
	}
	openReadyRemoteTab(t, a, RemoteTabOpenOptions{SessionID: "s1", SessionTitle: "First"})
	fs.mu.Lock()
	fs.failEnter = "session is already leased by another process"
	fs.mu.Unlock()
	if _, err := a.OpenRemoteProjectTab("box", "~/app", RemoteTabOpenOptions{SessionID: "s2",
		SessionTitle: "Second",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-observed:
		want := identity{"s1", oldPath, oldPath, "First"}
		if got != want {
			t.Fatalf("failure publication identity = %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rejection did not publish a failure")
	}
}
