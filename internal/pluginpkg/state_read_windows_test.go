//go:build windows

package pluginpkg

import (
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
