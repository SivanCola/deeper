package main

import (
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestReadPauseHistoryAndTopic(t *testing.T) {
	p := &provider.ReadPause{ID: "run", Reads: []provider.PausedRead{{Path: "file", Reason: "no_progress"}}}
	rows, ok := historyLocalOnlyRows(provider.Message{LocalOnly: true, ReadPause: p})
	if !ok || len(rows) != 1 || rows[0].Code != event.TurnOutcomeIncompleteRead || rows[0].ReadPause.ID != "run" {
		t.Fatal("pause not restored as one notice")
	}
	if status, ok := topicStatusFromTurnDone(event.TurnOutcomeIncompleteRead); !ok || status != topicStatusPaused {
		t.Fatal("read pause classified as success")
	}
}

func TestReadPauseSurvivesCurrentColdHistory(t *testing.T) {
	app, tab := currentBoundHistoryFixture(t, []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "read all"},
		{Role: provider.RoleAssistant, Content: "candidate"},
		{Role: provider.RoleTool, LocalOnly: true, ReadPause: &provider.ReadPause{ID: "run", Reads: []provider.PausedRead{{Path: "fixture.txt", Reason: "no_progress"}}}},
	})
	count := 0
	for _, row := range currentHistoryMessagesForTab(t, app, tab.ID) {
		if row.ReadPause != nil && row.ReadPause.ID == "run" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("pause count=%d", count)
	}
}
