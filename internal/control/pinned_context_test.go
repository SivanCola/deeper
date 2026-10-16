package control

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestPinnedContextNeverChangesBasePrompt(t *testing.T) {
	dir := t.TempDir()
	exec := agent.New(nil, nil, agent.NewSession("unframed system"), agent.Options{}, event.Discard)
	ctrl := newCurrentOwnedTestController(t, Options{
		Runner:       exec,
		Executor:     exec,
		SystemPrompt: "BASE",
		SessionDir:   dir,

		Sink: event.Discard,
	})
	if got := controlSystemMessage(ctrl.History()); got != "BASE" {
		t.Fatalf("host system prompt = %q", got)
	}
	if reasons := exec.Session().DrainContentRewriteReasons(); !slices.Contains(reasons, "host-system-prompt") {
		t.Fatalf("rewrite reasons = %v", reasons)
	}

	ctrl.ApplyExtensionSystemPrompt("EXTENSION")
	if got := ctrl.SystemPrompt(); got != "EXTENSION" {
		t.Fatalf("SystemPrompt = %q", got)
	}
	if got := controlSystemMessage(ctrl.History()); got != "EXTENSION" {
		t.Fatalf("extension system prompt = %q", got)
	}

	if err := ctrl.NewSession(); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := controlSystemMessage(ctrl.History()); got != "EXTENSION" {
		t.Fatalf("new session system prompt = %q", got)
	}
}

func TestPinnedContextLoaderAppendsAtAdmittedTurns(t *testing.T) {
	prov := &recordingProvider{streams: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: "one"}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "two"}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "three"}, {Type: provider.ChunkDone}},
	}}
	exec := agent.New(prov, nil, agent.NewSession("BASE"), agent.Options{}, event.Discard)
	content := "A"
	loads := 0
	var sessionPath string
	ctrl := newCurrentOwnedTestController(t, Options{
		Runner:       exec,
		Executor:     exec,
		SystemPrompt: "BASE",
		PinnedContextLoader: func(_ context.Context, path string) (agent.PinnedContextSnapshot, error) {
			loads++
			if path != sessionPath {
				t.Fatalf("loader path = %q", path)
			}
			return agent.PinnedContextSnapshot{Files: []agent.PinnedContextFile{{Path: "a.md", Content: content}}}, nil
		},
		Sink: event.Discard,
	})
	var err error
	sessionPath, err = ctrl.PinnedContextPath(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Run(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Run(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	content = "B"
	if err := ctrl.Run(context.Background(), "third"); err != nil {
		t.Fatal(err)
	}
	if loads != 3 {
		t.Fatalf("loader calls = %d", loads)
	}
	if got := controlSystemMessage(ctrl.History()); got != "BASE" {
		t.Fatalf("system prompt changed: %q", got)
	}
	revisions := 0
	ref, _ := ctrl.SessionRef()
	snapshot, err := ctrl.SessionService().Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range snapshot.Projection.Messages {
		if agent.IsPinnedContextRevision(message) {
			revisions++
		}
	}
	if revisions != 2 {
		t.Fatalf("revision messages = %d, want 2", revisions)
	}
	if len(prov.requests) != 3 {
		t.Fatalf("provider requests = %d", len(prov.requests))
	}
	for i := 1; i < len(prov.requests); i++ {
		previous := prov.requests[i-1].Messages
		current := prov.requests[i].Messages
		if len(current) < len(previous) || !reflect.DeepEqual(current[:len(previous)], previous) {
			t.Fatalf("request %d is not prefixed by request %d", i, i-1)
		}
	}
}
