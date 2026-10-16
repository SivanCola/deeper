package main

import (
	"context"
	"deepzora/internal/processidentity"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/provider"
)

func TestMarkLocalTakeoverSpectatorPublishesAndPersistsReclaimState(t *testing.T) {
	app := NewApp()
	tab := &WorkspaceTab{ID: "spectator", Scope: "global", ReadOnly: true}
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.mu.Unlock()

	app.markLocalTakeoverSpectator(tab)
	meta := app.tabMeta(tab, true)
	if !tab.Takeover.Spectator || !meta.TakenOver || !meta.ReadOnly {
		t.Fatalf("spectator state = tab=%v meta=%+v", tab.Takeover.Spectator, meta)
	}
	app.mu.Lock()
	_, entries, _, _ := app.saveTabsCollectLocked()
	app.mu.Unlock()
	if len(entries) != 1 || !entries[0].TakeoverSpectator || !entries[0].ReadOnly {
		t.Fatalf("persisted spectator entries = %+v", entries)
	}
}

func TestTakeoverSessionPromotesCurrentSpectator(t *testing.T) {
	for _, failBuild := range []bool{false, true} {
		t.Run(fmt.Sprintf("build-failure=%t", failBuild), func(t *testing.T) {
			app, oldCtrl, runtime, _, route := newCanonicalTitleFixture(t)
			installNoopRuntimeEvents(app)
			tab := app.tabs["test"]
			tab.ReadOnly, tab.Takeover.Spectator = true, true
			if err := oldCtrl.ReleaseSessionForHandoff(); err != nil {
				t.Fatal(err)
			}
			if err := oldCtrl.SessionService().Close(t.Context(), runtime.Ref()); err != nil {
				t.Fatal(err)
			}
			remote, err := oldCtrl.SessionService().EnsureExecution(t.Context(), runtime.Ref())
			if err != nil {
				t.Fatal(err)
			}
			appendSessionTestMessage(t, remote.Runtime(), "remote-latest", provider.Message{ID: "remote-latest", Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "remote latest"})
			if err := remote.Release(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := oldCtrl.SessionService().Close(t.Context(), runtime.Ref()); err != nil {
				t.Fatal(err)
			}
			ended := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/handoff":
					_ = json.NewEncoder(w).Encode(takeoverGrant{SessionPath: route, MirrorID: "mirror", SourceWriterID: "serve", TargetWriterID: processidentity.WriterID()})
				case "/mirror-end":
					select {
					case ended <- struct{}{}:
					default:
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer srv.Close()
			originalFind := takeoverFindTargetForTest
			takeoverFindTargetForTest = func(context.Context, *App, string) (takeoverServeRecord, *http.Client, SessionTakeoverView, error) {
				return takeoverServeRecord{base: srv.URL}, srv.Client(), SessionTakeoverView{Holder: "serve"}, nil
			}
			t.Cleanup(func() {
				takeoverFindTargetForTest = originalFind
				app.stopTakeoverMirrors()
				if c := tab.Ctrl; c != nil {
					c.Close()
				}
			})
			if failBuild {
				app.rebindCandidateHook = func(stage string) error { return errors.New("injected candidate build failure") }
			}
			err = app.TakeoverSession(tab.ID, "wait")
			if failBuild {
				if err == nil || tab.Ctrl != oldCtrl || !tab.ReadOnly || !tab.Takeover.Spectator {
					t.Fatalf("failed promotion changed spectator: err=%v", err)
				}
				select {
				case <-ended:
				case <-time.After(3 * time.Second):
					t.Fatal("failed grant not returned")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tab.Ctrl == oldCtrl || tab.ReadOnly || tab.Takeover.Spectator {
				t.Fatal("current runtime was not promoted")
			}
			found := false
			for _, msg := range tab.Ctrl.History() {
				if msg.Content == "remote latest" {
					found = true
				}
			}
			if !found {
				t.Fatal("promotion lost remote history")
			}
			mirror := app.takeoverMirrorForKey(sessionRuntimeKey(route))
			if mirror == nil {
				t.Fatal("promotion did not install mirror")
			}
			if err := mirror.returnLeaseForDemotion(tab); err != nil {
				t.Fatal(err)
			}
			if _, bound := tab.Ctrl.(control.IdentityLifecycle).SessionRef(); bound {
				t.Fatal("returned controller retained identity")
			}
			if _, running := oldCtrl.SessionService().Runtime(runtime.Ref()); running {
				t.Fatal("returned runtime still owns writer")
			}
		})
	}
}
