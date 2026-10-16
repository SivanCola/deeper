package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

// capturingProvider records the exact message list of every request it
// receives, marshaled at capture time, so tests can compare request bytes.
type capturingProvider struct {
	mu       sync.Mutex
	requests [][]byte
}

func (p *capturingProvider) Name() string { return "capturing" }

func (p *capturingProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	b, err := json.Marshal(req.Messages)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.requests = append(p.requests, b)
	p.mu.Unlock()
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	close(ch)
	return ch, nil
}

func (p *capturingProvider) lastRequestMessages(t *testing.T) []provider.Message {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		t.Fatal("provider captured no requests")
	}
	var msgs []provider.Message
	if err := json.Unmarshal(p.requests[len(p.requests)-1], &msgs); err != nil {
		t.Fatalf("unmarshal captured request: %v", err)
	}
	return msgs
}

// marshalMessages drops message ids first: they are local transcript identity
// that provider adapters never copy to the wire, and a freshly composed
// follow-up legitimately mints a new one on every run.
func marshalMessages(t *testing.T, msgs []provider.Message) []byte {
	t.Helper()
	msgs = append([]provider.Message(nil), msgs...)
	for i := range msgs {
		msgs[i].ID = ""
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	return b
}

func TestRebindReproducesRequestBytes(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
	service, err := session.NewService("local", session.NewFilesystemPersistence(filepath.Join(dir, "current")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	const systemPrompt = "SYSPROMPT stable bytes"

	prov := &capturingProvider{}
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrl := newFixtureController(t, control.Options{Runner: exec, Executor: exec, SystemPrompt: systemPrompt, SessionDir: dir, SessionService: service, ExclusiveSession: true, Label: "test", Sink: event.Discard})

	ref, err := ctrl.BindFreshSession(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RunTurn(context.Background(), "first question"); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if err := ctrl.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// Copy after turn one so both controllers send the same next turn.
	copied, err := service.CopySession(t.Context(), session.CopyRequest{Source: ref, ChildID: "rebound", OperationID: "copy-rebind"})
	if err != nil {
		t.Fatal(err)
	}

	// Baseline: the follow-up turn on the ORIGINAL controller — the exact
	// request an uninterrupted (cache-warm) session would send.
	if err := ctrl.RunTurn(context.Background(), "second question"); err != nil {
		t.Fatalf("baseline second turn: %v", err)
	}
	baseline := prov.lastRequestMessages(t)
	baselineBytes := marshalMessages(t, baseline)
	if len(baseline) < 4 {
		t.Fatalf("baseline request has %d messages, want system + first exchange + follow-up", len(baseline))
	}

	// Open the copied current session on a new controller.
	prov2 := &capturingProvider{}
	exec2 := agent.New(prov2, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrl2 := newFixtureController(t, control.Options{Runner: exec2, Executor: exec2, SystemPrompt: systemPrompt, SessionDir: dir, SessionService: service, ExclusiveSession: true, Label: "test", Sink: event.Discard})
	if _, err := ctrl2.OpenSession(t.Context(), copied.Child); err != nil {
		t.Fatal(err)
	}

	if err := ctrl2.RunTurn(context.Background(), "second question"); err != nil {
		t.Fatalf("post-rebind second turn: %v", err)
	}
	rebound := prov2.lastRequestMessages(t)
	reboundBytes := marshalMessages(t, rebound)
	if string(reboundBytes) != string(baselineBytes) {
		t.Fatalf("rebind changed the request bytes — the provider prefix cache is invalidated:\nbaseline: %s\nrebound:  %s", baselineBytes, reboundBytes)
	}
}

func TestRebindPreservesCurrentPersistedSystemPrompt(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
	service, err := session.NewService("local", session.NewFilesystemPersistence(filepath.Join(dir, "current")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })

	prov := &capturingProvider{}
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession("SYSPROMPT v1"), agent.Options{}, event.Discard)
	ctrl := newFixtureController(t, control.Options{Runner: exec, Executor: exec, SystemPrompt: "SYSPROMPT v1", SessionDir: dir, SessionService: service, ExclusiveSession: true, Label: "test", Sink: event.Discard})
	ref, err := ctrl.BindFreshSession(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RunTurn(context.Background(), "first question"); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if err := ctrl.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	copied, err := service.CopySession(t.Context(), session.CopyRequest{Source: ref, ChildID: "rebound", OperationID: "copy-rebind"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RunTurn(context.Background(), "second question"); err != nil {
		t.Fatalf("baseline second turn: %v", err)
	}
	baseline := prov.lastRequestMessages(t)
	baselineBytes := marshalMessages(t, baseline)

	prov2 := &capturingProvider{}
	exec2 := agent.New(prov2, tool.NewRegistry(), agent.NewSession("SYSPROMPT v2 drifted"), agent.Options{}, event.Discard)
	ctrl2 := newFixtureController(t, control.Options{Runner: exec2, Executor: exec2, SystemPrompt: "SYSPROMPT v2 drifted", SessionDir: dir, SessionService: service, ExclusiveSession: true, Label: "test", Sink: event.Discard})
	if _, err := ctrl2.OpenSession(t.Context(), copied.Child); err != nil {
		t.Fatal(err)
	}
	if err := ctrl2.RunTurn(context.Background(), "second question"); err != nil {
		t.Fatalf("post-rebind turn: %v", err)
	}
	rebound := prov2.lastRequestMessages(t)
	if string(marshalMessages(t, rebound)) != string(baselineBytes) {
		t.Fatal("current persisted system prompt changed during rebind")
	}
}
