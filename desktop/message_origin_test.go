package main

import (
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
)

func TestCurrentMessageOriginIsAuthoritativeAcrossDesktopHistory(t *testing.T) {
	quotedHostText := "[Completion validation continuation]" + " explain what this message means"
	msgs := []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "wrapped", RawContent: quotedHostText},
		{Role: provider.RoleAssistant, Content: "answer"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: agent.MidTurnSteerPrefix + "\nuse the smaller patch", RawContent: "use the smaller patch"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "innocent looking continuation"},
	}

	if got := visibleHistoryUserTurns(msgs, identityPromptDisplay); got != 1 {
		t.Fatalf("visible user turns = %d, want only the explicitly user-authored turn", got)
	}
	history := historyMessagesFromProvider(msgs, identityPromptDisplay, map[int]int{0: 7, 2: 8, 3: 9})
	if len(history) != 3 || history[0].Role != "user" || history[0].Content != quotedHostText ||
		history[2].Role != "notice" || history[2].Content != "↪ use the smaller patch" {
		t.Fatalf("history = %+v, want quoted host text plus one steer notice", history)
	}
	if history[0].CheckpointTurn == nil || *history[0].CheckpointTurn != 7 || history[2].CheckpointTurn != nil {
		t.Fatalf("checkpoint ownership = %+v, want only the authored user turn to own checkpoint 7", history)
	}
}
