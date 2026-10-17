package agentpreset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListDoesNotOfferMalformedProjectOverride(t *testing.T) {
	dirs := Dirs{User: t.TempDir(), Project: t.TempDir()}
	writePreset(t, dirs.User, "review", "User definition")
	writePreset(t, dirs.Project, "review", "---\ntool: [read_file]\n---\nbroken override")
	list, problems := List(dirs)
	if len(list) != 1 || len(problems) != 1 {
		t.Fatalf("malformed override list=%+v problems=%v", list, problems)
	}
	if _, err := Resolve(dirs, "review"); err == nil {
		t.Fatal("Resolve accepted malformed project override")
	}
}

func TestRemoveRequiresAuthoringRoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writePreset(t, dir, "review", "keep me")
	if err := Remove("", "review"); err == nil {
		t.Fatal("Remove accepted empty directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "review.md")); err != nil {
		t.Fatalf("empty root removed cwd preset: %v", err)
	}
}

func TestPresetJSONPreservesEmptyToolCeiling(t *testing.T) {
	for _, tc := range []struct {
		tools []string
		want  string
	}{{nil, `"tools":null`}, {[]string{}, `"tools":[]`}} {
		data, err := json.Marshal(Preset{ID: "review", Tools: tc.tools})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), tc.want) || !strings.Contains(string(data), `"keepCoding":`) {
			t.Fatalf("preset JSON=%s", data)
		}
	}
}
