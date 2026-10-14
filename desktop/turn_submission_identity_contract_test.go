package main

import (
	"strings"
	"testing"
)

func TestCurrentSubmissionEntriesRequireExplicitIdentity(t *testing.T) {
	app := NewApp()
	entries := map[string]func(string, string) error{
		"plain":       func(tab, id string) error { return app.SubmitToTabWithID(tab, "input", id) },
		"display":     func(tab, id string) error { return app.SubmitDisplayToTabWithID(tab, "display", "input", id) },
		"invocations": func(tab, id string) error { return app.SubmitInvocationsToTabWithID(tab, "display", "input", nil, id) },
		"edited": func(tab, id string) error {
			return app.SubmitEditedDisplayToTabWithID(tab, "display", "input", "original", id)
		},
		"goal": func(tab, id string) error {
			_, err := app.SubmitInitialGoalToTabWithID(tab, "goal", "display", "input", nil, "normal", "read-only", id)
			return err
		},
		"receipt": func(tab, id string) error { _, err := app.StartTurnForTab(tab, "input", id); return err },
		"drafts":  func(tab, id string) error { _, err := app.StartTurnForTabWithDrafts(tab, "input", id, nil); return err },
	}
	for name, entry := range entries {
		t.Run(name, func(t *testing.T) {
			if err := entry("", "id"); err == nil || !strings.Contains(err.Error(), "tabId is required") {
				t.Fatalf("missing tab identity: %v", err)
			}
			if err := entry("tab", " "); err == nil || !strings.Contains(err.Error(), "submissionId is required") {
				t.Fatalf("missing submission identity: %v", err)
			}
		})
	}
}
