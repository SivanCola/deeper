package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHistoricalEventHistoryRejected(t *testing.T) {
	for _, kindKey := range []string{"kind", "type"} {
		t.Run(kindKey, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "old.jsonl")
			body := []byte(`{"` + kindKey + `":"user.message","text":"old prompt"}` + "\n")
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			if rows, err := previewSessionMessages(dir, path); err == nil || len(rows) != 0 {
				t.Fatalf("historical preview accepted: rows=%+v err=%v", rows, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if rows, err := collectPromptHistoryEntries(path, info, identityPromptDisplay); err == nil || len(rows) != 0 {
				t.Fatalf("historical prompt history accepted: rows=%+v err=%v", rows, err)
			}
			app := historySliceTestApp(t)
			if rows, err := app.coldHistorySlice(dir, path, HistorySliceRequest{Turns: 4, Entries: 6}); err == nil || len(rows.Entries) != 0 {
				t.Fatalf("historical paging accepted: rows=%+v err=%v", rows, err)
			}
		})
	}
	if _, _, ok := parseHistoryEntryID("sold:r0:e0:o0"); ok {
		t.Fatal("historical event row identity accepted")
	}
}
