// Package testutil holds test helpers shared by packages that cannot import
// each other's _test files.
package testutil

import (
	"encoding/json"
	"testing"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/provider"
)

// WaitFor polls cond until it holds or the timeout elapses.
func WaitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ToolCallChunk builds a provider chunk carrying a tool call.
func ToolCallChunk(id, name, args string) provider.Chunk {
	return provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: id, Name: name, Arguments: args}}
}

// MustJSON marshals value, failing the test on error.
func MustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// FindPlugin returns the plugin entry with the given name.
func FindPlugin(entries []config.PluginEntry, name string) (config.PluginEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return config.PluginEntry{}, false
}
