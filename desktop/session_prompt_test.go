package main

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func TestParallelDesktopTabsPersistCompleteTranscriptsAcrossReload(t *testing.T) {
	isolateDesktopUserDirs(t)
	const (
		tabCount    = 12
		turnsPerTab = 6
		systemText  = "stable desktop system prompt"
	)
	dir := t.TempDir()
	service, err := session.NewService("local", session.NewFilesystemPersistence(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	tabs := make(map[string]*WorkspaceTab, tabCount)
	tabOrder := make([]string, 0, tabCount)
	controllers := make([]*control.Controller, 0, tabCount)

	for tabIndex := range tabCount {
		id := fmt.Sprintf("parallel-%02d", tabIndex)
		prov := &capturingProvider{}
		exec := agent.New(prov, tool.NewRegistry(), agent.NewSession(systemText), agent.Options{}, event.Discard)
		ctrl := control.New(control.Options{
			Runner:         exec,
			Executor:       exec,
			SystemPrompt:   systemText,
			SessionDir:     dir,
			SessionService: service, ExclusiveSession: true,
			Label: id,
			Sink:  event.Discard,
		})
		if _, err := ctrl.BindFreshSession(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		controllers = append(controllers, ctrl)
		tabOrder = append(tabOrder, id)
		tabs[id] = &WorkspaceTab{
			ID:          id,
			Ctrl:        ctrl,
			Scope:       "global",
			SessionID:   id,
			Ready:       true,
			disabledMCP: map[string]ServerView{},
		}
	}
	app := &App{
		tabs:        tabs,
		tabOrder:    tabOrder,
		activeTabID: tabOrder[0],
	}

	start := make(chan struct{})
	errs := make(chan error, tabCount+1)
	var wg sync.WaitGroup
	for tabIndex, ctrl := range controllers {
		wg.Go(func() {
			<-start
			for turn := range turnsPerTab {
				input := fmt.Sprintf("tab-%02d-turn-%02d", tabIndex, turn)
				if err := ctrl.RunTurn(context.Background(), input); err != nil {
					errs <- fmt.Errorf("%s: %w", input, err)
					return
				}
			}
		})
	}
	wg.Go(func() {
		<-start
		for range 3 {
			for _, id := range tabOrder {
				if err := app.SetActiveTab(id); err != nil {
					errs <- fmt.Errorf("switch to %s: %w", id, err)
					return
				}
			}
		}
	})

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		for _, ctrl := range controllers {
			ctrl.Close()
		}
		return
	}

	for _, ctrl := range controllers {
		if err := ctrl.Snapshot(); err != nil {
			t.Fatalf("final snapshot %s: %v", ctrl.Label(), err)
		}
		ctrl.Close()
	}

	for tabIndex, id := range tabOrder {
		reloaded, err := service.Query().Snapshot(t.Context(), session.SessionRef{HostID: service.HostID(), SessionID: id})
		if err != nil {
			t.Fatalf("LoadSession %s: %v", id, err)
		}
		msgs := reloaded.Projection.ModelMessages
		wantMessages := 1 + turnsPerTab*2
		if len(msgs) != wantMessages {
			t.Fatalf("%s message count = %d, want %d: %+v", id, len(msgs), wantMessages, msgs)
		}
		if msgs[0].Role != provider.RoleSystem || msgs[0].Content != systemText {
			t.Fatalf("%s system message = %+v", id, msgs[0])
		}
		for turn := range turnsPerTab {
			user := msgs[1+turn*2]
			assistant := msgs[2+turn*2]
			wantUser := fmt.Sprintf("tab-%02d-turn-%02d", tabIndex, turn)
			if user.Role != provider.RoleUser || agent.StripTransientUserBlocks(user.Content) != wantUser {
				t.Fatalf("%s turn %d user = %+v, want %q", id, turn, user, wantUser)
			}
			if assistant.Role != provider.RoleAssistant || assistant.Content != "ok" {
				t.Fatalf("%s turn %d assistant = %+v, want ok", id, turn, assistant)
			}
		}
	}
}
