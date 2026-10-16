package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/historycatalog"
	"deepzora/internal/session"
)

func TestHistoryReindexUsesCurrentMainAndChildCatalogs(t *testing.T) {
	t.Setenv("DEEPZORA_CACHE_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "sessions")
	root := session.RootForSessionDir(dir)
	createCanonicalTestSession(t, root, "same-id", "zebraunicorn parent")
	createCanonicalTestSession(t, filepath.Join(root, ".subagents"), "same-id", "zebraunicorn child")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dir, "old.jsonl")
	old := []byte("unsupported old transcript with zebraunicorn\n")
	if err := os.WriteFile(oldPath, old, 0600); err != nil {
		t.Fatal(err)
	}
	paths := []string{oldPath, filepath.Join(root, "same-id", "events.frames"), filepath.Join(root, ".subagents", "same-id", "events.frames")}
	before := map[string][]byte{}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = body
	}
	out := captureStdout(t, func() {
		if code := reindexHistoryCatalog([]string{"--dir", dir, "--dir", dir, "--json"}); code != 0 {
			t.Fatalf("current reindex exit=%d", code)
		}
	})
	var status historycatalog.Status
	if err := json.Unmarshal([]byte(out), &status); err != nil || status.State != "ready" || status.Indexed != 2 {
		t.Fatalf("reindex status: %+v, %v", status, err)
	}
	catalog, err := historycatalog.Open(t.Context(), historycatalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close(context.Background())
	result, err := catalog.Search(t.Context(), historycatalog.SearchRequest{Query: "zebraunicorn"})
	if err != nil || len(result.Items) != 2 || result.Items[0].SessionPath == result.Items[1].SessionPath {
		t.Fatalf("main/child identity collision or missing result: %+v, %v", result, err)
	}
	for _, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before[path], after) {
			t.Fatalf("reindex modified source %s: %v", path, err)
		}
	}
}
