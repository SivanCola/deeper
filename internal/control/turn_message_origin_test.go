package control

import (
	"context"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func TestTurnOrchestratorUserTextMatchingRetiredSyntheticPrefixKeepsUserOrigin(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSession("sys")
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("done")}}
	exec := agent.New(prov, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Runner: exec, Executor: exec, SessionDir: dir, Label: "test"})

	raw := "[Completion validation continuation]" + " give me a plan only"

	if err := newTurnOrchestrator(c).runTurnWithRawDisplay(context.Background(), raw, raw, ""); err != nil {
		t.Fatal(err)
	}

	msgs := sess.Snapshot()
	if len(msgs) < 2 || msgs[1].Role != provider.RoleUser || msgs[1].Origin != provider.MessageOriginUser || msgs[1].RawContent != raw {
		t.Fatalf("persisted user message = %+v, want explicit user origin and raw content", msgs)
	}
	if cps := c.Checkpoints(); len(cps) != 1 || cps[0].Prompt != raw {
		t.Fatalf("checkpoints = %+v, want a real user checkpoint", cps)
	}
}

func TestComposedSyntheticTurnPersistsHostOriginWithoutKeywordFallback(t *testing.T) {
	sess := agent.NewSession("sys")
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("done")}}
	exec := agent.New(prov, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Runner: exec, Executor: exec, Label: "test"})

	const synthetic = "ordinary looking continuation"
	if IsSyntheticUserMessage(synthetic) {
		t.Fatal("test setup: synthetic text must not match the legacy classifier")
	}
	if err := newTurnOrchestrator(c).runComposedSyntheticTurn(context.Background(), synthetic); err != nil {
		t.Fatal(err)
	}

	msgs := sess.Snapshot()
	if len(msgs) < 2 || msgs[1].Origin != provider.MessageOriginHost || msgs[1].RawContent != synthetic {
		t.Fatalf("persisted synthetic message = %+v, want explicit host origin and raw content", msgs)
	}
}
