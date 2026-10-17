package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"deepzora/internal/control"
)

func TestDesktopAgentPresetSelectionAcceptsFirstSubmission(t *testing.T) {
	a := newManualSessionTestApp(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	request := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		request <- body
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	if _, err := a.SaveProviderWithKey(ProviderView{Name: "preset-submit", Kind: "openai", BaseURL: server.URL, Models: []string{"test"}, NoProxy: true}, "disposable-fixture"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetDefaultModel("preset-submit/test"); err != nil {
		t.Fatal(err)
	}
	tab, _ := manualAgentPresetTab(t, a, "default")
	if _, err := a.SaveAgentPreset(tab.ID, "user", "review", "---\ntools: []\n---\nNATIVE_PERSONA"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetAgentPresetForTab(tab.ID, "review"); err != nil {
		t.Fatal(err)
	}
	if err := a.SubmitToTabWithID(tab.ID, "first preset turn", "first-preset-submit"); err != nil {
		t.Fatalf("first submission rejected: %v", err)
	}
	select {
	case <-request:
	case <-ctx.Done():
		ctrl := a.controllerForTab(tab).(*control.Controller)
		t.Fatalf("selected preset did not dispatch first turn: status=%+v inbox=%+v", ctrl.RuntimeStateSnapshot(), ctrl.InboxSnapshot())
	}
}
