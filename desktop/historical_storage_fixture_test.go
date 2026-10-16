package main

import (
	"encoding/json"
	"os"
	"testing"
)

// Inert historical input for regressions proving that no current operation
// discovers, imports, or rewrites retired file sessions.
func writeHistoricalMetadata(t *testing.T, path string, fields map[string]any) error {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return os.WriteFile(path+".meta", data, 0o600)
}
