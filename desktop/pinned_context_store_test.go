package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPinnedContextSidecarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source", "pinned-context.json")
	files := []string{"docs/b.md", "docs/a.md", "docs/b.md"}
	if err := savePinnedContextState(source, files); err != nil {
		t.Fatalf("save source: %v", err)
	}
	state, err := loadPinnedContextState(source)
	if err != nil {
		t.Fatalf("load source: %v", err)
	}
	want := []string{"docs/a.md", "docs/b.md"}
	if !reflect.DeepEqual(state.Files, want) {
		t.Fatalf("source files = %v, want stable sorted order %v", state.Files, want)
	}
	if state.SchemaVersion != pinnedContextSchemaVersion || state.SessionID != "source" {
		t.Fatalf("source state = %+v", state)
	}
}

func TestPinnedContextSidecarRejectsCorruptionAndUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pinned-context.json")
	sidecar := path
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "corrupt", raw: "{"},
		{name: "unknown schema", raw: `{"schemaVersion":2,"sessionId":"session","files":[]}`},
		{name: "wrong session", raw: `{"schemaVersion":1,"sessionId":"other","files":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(sidecar, []byte(test.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadPinnedContextState(path); err == nil {
				t.Fatal("invalid sidecar was accepted")
			}
		})
	}
}
