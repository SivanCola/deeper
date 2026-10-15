package main

import (
	"testing"
	"testing/synctest"
)

func TestPromptHistoryTapeIdentityDoesNotDependOnClockTicks(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	dirA, dirB := t.TempDir(), t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		first, err := app.newPromptHistoryTape("project", dirA, "")
		if err != nil {
			t.Fatal(err)
		}
		second, err := app.newPromptHistoryTape("project", dirB, "")
		if err != nil {
			t.Fatal(err)
		}
		if first.nonce == second.nonce {
			t.Fatal("different prompt-history tapes shared an identity within one clock tick")
		}
	})
}
