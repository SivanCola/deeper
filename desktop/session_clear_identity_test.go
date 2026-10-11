package main

import (
	"deepzora/internal/session"
	"testing"
)

func TestClearSessionForTabReturnsReplacementIdentity(t *testing.T) {
	app, tab, _, _, _ := canonicalWorkspaceOpenFixture(t)
	oldID, before := tab.SessionID, tab.SessionGeneration
	tab.setPinnedFiles([]string{"README.md"})
	result, err := app.ClearSessionForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID == "" || result.SessionID == oldID || result.SessionPath != "" {
		t.Fatalf("replacement identity = %+v", result)
	}
	if result.SessionGeneration <= before || tab.SessionGeneration != result.SessionGeneration {
		t.Fatal("clear did not advance its generation")
	}
	if tab.SessionID != result.SessionID || len(tab.GetPinnedFiles()) != 0 {
		t.Fatal("replacement inherited the cleared session")
	}
	page, err := app.SessionHistoryWindowForTab(tab.ID, session.HistoryWindowRequest{Anchor: "newest"})
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("clear retained messages: %+v %v", page, err)
	}
}
