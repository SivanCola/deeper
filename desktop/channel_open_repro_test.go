package main

import (
	"strings"
	"testing"
)

func TestChannelSessionUsesCanonicalIdentityAndIsReadOnly(t *testing.T) {
	app, tab, _, _, _ := canonicalWorkspaceOpenFixture(t)
	if _, err := app.OpenChannelSessionForTab(tab.ID, sessionRoute(tab.SessionID)); err != nil {
		t.Fatal(err)
	}
	if !tab.ReadOnly {
		t.Fatal("channel transcript is writable")
	}
	if err := app.SubmitToTab(tab.ID, "write"); err == nil {
		t.Fatal("read-only channel admitted a write")
	}
	if _, err := app.OpenChannelSessionForTab(tab.ID, "/old/session.jsonl"); err == nil || !strings.Contains(err.Error(), "durable") {
		t.Fatalf("native path accepted: %v", err)
	}
}
