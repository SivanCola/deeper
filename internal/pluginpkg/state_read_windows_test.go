//go:build windows

package pluginpkg

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateReaderPreservesLongPathsAndEncoding(t *testing.T) {
	home := t.TempDir()
	for len(home) < 300 {
		home = filepath.Join(home, strings.Repeat("long", 8))
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	missing, err := LoadState(home)
	if err != nil || missing.Version != 1 || len(missing.Plugins) != 0 {
		t.Fatalf("missing long-path state: %+v, %v", missing, err)
	}
	// State previously accepted UTF-16 files written by Windows tools.
	encoded := []byte{0xff, 0xfe}
	for _, c := range []byte(`{"version":1,"plugins":[{"name":"encoded","enabled":true}]}`) {
		encoded = append(encoded, c, 0)
	}
	if err := os.WriteFile(StatePath(home), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(home)
	if err != nil || len(state.Plugins) != 1 || state.Plugins[0].Name != "encoded" || !state.Plugins[0].Enabled {
		t.Fatalf("encoded long-path state: %+v, %v", state, err)
	}
}

func TestStateReaderAllowsAtomicReplacement(t *testing.T) {
	home := t.TempDir()
	old := State{Version: 1, Plugins: []InstalledPlugin{{Name: "old", Enabled: true}}}
	if err := SaveState(home, old); err != nil {
		t.Fatal(err)
	}
	reader, err := openStateFile(StatePath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	next := State{Version: 1, Plugins: []InstalledPlugin{{Name: "new", Enabled: false}}}
	if err := SaveState(home, next); err != nil {
		t.Fatalf("publish while old reader is open: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot State
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Plugins) != 1 || snapshot.Plugins[0].Name != "old" || !snapshot.Plugins[0].Enabled {
		t.Fatalf("open reader lost its complete old snapshot: %+v", snapshot)
	}
	current, err := LoadState(home)
	if err != nil || len(current.Plugins) != 1 || current.Plugins[0].Name != "new" || current.Plugins[0].Enabled {
		t.Fatalf("new reader did not see complete replacement: %+v, %v", current, err)
	}
}
