package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPurgeResumesDeeperReceiptWithoutAcceptingAnotherSession(t *testing.T) {
	for _, receiptID := range []string{"victim", "other"} {
		t.Run(receiptID, func(t *testing.T) {
			root := t.TempDir()
			staged := filepath.Join(root, ".purging", "victim")
			if err := os.MkdirAll(staged, 0700); err != nil {
				t.Fatal(err)
			}
			body := filepath.Join(staged, "history")
			if err := os.WriteFile(body, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(staged+".receipt", []byte("deeper-session-purge-v1\n"+receiptID+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			called := false
			err := NewFilesystemPersistence(root).PurgeWithTombstone(t.Context(), "victim", func() error { called = true; return nil })
			if receiptID == "victim" {
				if err != nil || !called {
					t.Fatalf("legacy recovery: %v, callback=%v", err, called)
				}
				if _, err := os.Stat(staged); !os.IsNotExist(err) {
					t.Fatalf("staging retained: %v", err)
				}
			} else {
				if err == nil || called {
					t.Fatalf("wrong-session receipt accepted: %v, callback=%v", err, called)
				}
				if content, err := os.ReadFile(body); err != nil || string(content) != "keep" {
					t.Fatalf("unowned data changed: %s %v", content, err)
				}
			}
		})
	}
}
