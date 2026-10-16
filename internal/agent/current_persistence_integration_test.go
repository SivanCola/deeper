package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"deepzora/internal/agent/testutil"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
	"deepzora/internal/tool/builtin"
)

// Deterministic current-store regressions run in the default test suite.
func TestCurrentPersistenceRoundTrip(t *testing.T) {
	state := newCurrentPersistenceSession(t, NewSession("system"))
	identity := state.handle.(SessionCacheIdentity).ProviderCacheSessionIdentity()
	p := testutil.NewMock("current-store", testutil.Turn{Text: "first answer"}, testutil.Turn{Text: "second answer"})
	a := New(p, nil, state.conversation(), Options{SessionCheckpointer: state.handle}, event.Discard)
	if err := a.Run(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	conversation := state.reopen(t, state.ref)
	if state.handle.(SessionCacheIdentity).ProviderCacheSessionIdentity() != identity {
		t.Fatal("reopen changed cache identity")
	}
	if messages := conversation.Snapshot(); len(messages) != 3 || messages[2].Content != "first answer" {
		t.Fatalf("restored messages: %+v", messages)
	}
	a = New(p, nil, conversation, Options{SessionCheckpointer: state.handle}, event.Discard)
	if err := a.Run(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	if err := state.handle.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	messages, err := state.store.Read(t.Context(), state.ref)
	if err != nil || len(messages) != 5 || messages[4].Content != "second answer" {
		t.Fatalf("current history: %+v %v", messages, err)
	}
}

func TestCurrentPersistenceParallelToolMetadata(t *testing.T) {
	state := newCurrentPersistenceSession(t, NewSession("system"))
	var count atomic.Int32
	registry := tool.NewRegistry()
	registry.Add(recoveryEchoTool{executions: &count})
	calls := make([]provider.ToolCall, 12)
	for i := range calls {
		calls[i] = provider.ToolCall{ID: NewMessageID(), Name: "echo", Arguments: `{}`}
	}
	p := testutil.NewMock("parallel-current", testutil.Turn{ToolCalls: calls}, testutil.Turn{Text: "done"})
	a := New(p, registry, state.conversation(), Options{SessionCheckpointer: state.handle}, event.Discard)
	if err := a.Run(t.Context(), "read in parallel"); err != nil {
		t.Fatal(err)
	}
	if err := state.handle.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	messages, err := state.store.Read(t.Context(), state.ref)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Recovery == nil || call.Recovery.State != provider.ToolRunCompleted {
				t.Fatalf("lost settled call metadata: %+v", call)
			}
			seen++
		}
	}
	if seen != len(calls) || int(count.Load()) != len(calls) {
		t.Fatalf("calls=%d executions=%d", seen, count.Load())
	}
}

type failedIntentRecorder struct{ SubagentTranscript }

func (r failedIntentRecorder) RecordSessionMessageUpsert(ctx context.Context, reason string, message provider.Message) error {
	if reason == "write-intent" {
		return errors.New("intent persistence failed")
	}
	return r.SubagentTranscript.RecordSessionMessageUpsert(ctx, reason, message)
}

func TestCurrentPersistenceFailureBlocksFileWrite(t *testing.T) {
	state := newCurrentPersistenceSession(t, NewSession("system"))
	root := t.TempDir()
	path := filepath.Join(root, "blocked.txt")
	registry := tool.NewRegistry()
	for _, writer := range builtin.ConfineWriters([]string{root}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}) {
		if writer.Name() == "write_file" {
			registry.Add(writer)
		}
	}
	args, _ := json.Marshal(map[string]string{"path": path, "content": "must not appear"})
	p := testutil.NewMock("blocked-current", testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "write", Name: "write_file", Arguments: string(args)}}}, testutil.Turn{Text: "blocked"})
	a := New(p, registry, state.conversation(), Options{SessionCheckpointer: failedIntentRecorder{state.handle}}, event.Discard)
	_ = a.Run(t.Context(), "write the file")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file changed after failed intent commit: %v", err)
	}
}

type recoveryEchoTool struct{ executions *atomic.Int32 }

func (t recoveryEchoTool) Name() string        { return "echo" }
func (t recoveryEchoTool) Description() string { return "Return a fixed live-test marker." }
func (t recoveryEchoTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (t recoveryEchoTool) ReadOnly() bool { return true }
func (t recoveryEchoTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.executions.Add(1)
	return "live recovery marker", nil
}
