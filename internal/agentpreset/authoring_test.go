package agentpreset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestPresetJSONDefaultsOlderHostContext(t *testing.T) {
	var older struct {
		Presets []Preset `json:"presets"`
		Current Preset   `json:"current"`
	}
	data := `{"presets":[{"id":"default","keepCoding":true,"tools":null},{"id":"review","keepCoding":true,"tools":[]}],"current":{"id":"review","keepCoding":true,"tools":[]}}`
	if err := json.Unmarshal([]byte(data), &older); err != nil {
		t.Fatal(err)
	}
	for _, p := range append(older.Presets, older.Current) {
		if !p.IncludeRuntimeContext || p.Complete || !p.KeepCoding {
			t.Errorf("older DTO changed prompt defaults: %+v", p)
		}
	}
	if older.Presets[0].Tools != nil || older.Presets[1].Tools == nil || older.Current.Tools == nil {
		t.Fatalf("older DTO changed tool ceilings: %+v", older)
	}
	for _, context := range []bool{false, true} {
		in := Preset{ID: "complete", Body: "Persona", Complete: true, KeepCoding: true, IncludeRuntimeContext: context}
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var got Preset
		if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, in) {
			t.Fatalf("JSON round trip = %+v, %v; want %+v", got, err, in)
		}
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
		if !strings.Contains(string(data), tc.want) || !strings.Contains(string(data), `"keepCoding":`) || !strings.Contains(string(data), `"complete":false`) || !strings.Contains(string(data), `"includeRuntimeContext":false`) {
			t.Fatalf("preset JSON=%s", data)
		}
		var got Preset
		if err := json.Unmarshal(data, &got); err != nil || (got.Tools == nil) != (tc.tools == nil) || got.IncludeRuntimeContext {
			t.Fatalf("decoded preset=%+v, %v", got, err)
		}
	}
}
