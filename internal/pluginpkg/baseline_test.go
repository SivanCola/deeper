package pluginpkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeBaselineRejectsHistoricalManifestForms(t *testing.T) {
	for _, raw := range []string{
		"{\"name\":\"old\"}",
		"{\"apiVersion\":\"deeper.com/plugin/v2\",\"name\":\"old\"}",
		"{\"apiVersion\":\"reasonix.com/plugin/v2\",\"name\":\"old\"}",
		"{\"apiVersion\":\"deepzora.com/plugin/v2\",\"name\":\"old\",\"skills\":[\"skills\"]}",
	} {
		root := t.TempDir()
		path := filepath.Join(root, NativeManifest)
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ParseDir(root); err == nil {
			t.Fatalf("accepted old manifest %s", raw)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != raw {
			t.Fatal("rejected manifest changed")
		}
		if files, err := os.ReadDir(root); err != nil || len(files) != 1 {
			t.Fatal("parser wrote migration files")
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "deeper-plugin.json"), []byte("{\"name\":\"old\"}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseDir(root); err == nil || !strings.Contains(err.Error(), NativeManifest) {
		t.Fatalf("legacy filename: %v", err)
	}
}
