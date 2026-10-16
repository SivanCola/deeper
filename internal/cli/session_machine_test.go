package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestSessionMachineListIsStableAndRedacted(t *testing.T) {
	identityKey := installMachineTestIdentity(t)
	dir := t.TempDir()
	saveMachineTestSession(t, dir, "older")
	saveMachineTestSession(t, dir, "newer")

	var out bytes.Buffer
	if code := runSessionCommand([]string{"list", "--dir", dir, "--json"}, &out); code != 0 {
		t.Fatalf("list exit code = %d, output = %s", code, out.String())
	}
	var response machineSessionList
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if response.SchemaVersion != machineSchemaVersion || response.Command != "session.list" {
		t.Fatalf("header = %+v", response)
	}
	if len(response.Sessions) != 2 {
		t.Fatalf("sessions = %+v, want two sessions", response.Sessions)
	}
	if got := response.Sessions[0].ID; got != machineSessionIDWithKey("newer", identityKey) {
		t.Errorf("first session = %q, want opaque newer id", got)
	}
	if got := response.Sessions[1].ID; got != machineSessionIDWithKey("older", identityKey) {
		t.Errorf("second session = %q, want opaque older id", got)
	}
	if response.Sessions[0].Turns != 1 || response.Sessions[0].Scope != "project" {
		t.Errorf("session metadata = %+v", response.Sessions[0])
	}
	if strings.Contains(out.String(), "PRIVATE") || strings.Contains(out.String(), dir) {
		t.Fatalf("machine output leaked private content or path: %s", out.String())
	}
}

func TestMachineProjectSessionDirUsesProjectStore(t *testing.T) {
	projectRoot := t.TempDir()
	got := machineProjectSessionDir(projectRoot)
	want := config.ProjectSessionDir(projectRoot)
	if got != want {
		t.Fatalf("machine project session dir = %q, want %q", got, want)
	}
	if got == projectRoot {
		t.Fatalf("machine project session dir must not use the project root directly: %q", got)
	}
}

func TestSessionMachineProjectRootUsesProjectStore(t *testing.T) {
	identityKey := installMachineTestIdentity(t)
	projectRoot := t.TempDir()
	sessionDir := config.ProjectSessionDir(projectRoot)
	saveMachineTestSession(t, sessionDir, "project")

	var out bytes.Buffer
	if code := runSessionCommand([]string{"list", "--json", "--project-root", projectRoot}, &out); code != 0 {
		t.Fatalf("list exit code = %d, output = %s", code, out.String())
	}
	var response machineSessionList
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(response.Sessions) != 1 || response.Sessions[0].ID != machineSessionIDWithKey("project", identityKey) {
		t.Fatalf("sessions = %+v, want project session", response.Sessions)
	}
}

func TestSessionMachineShowAndStatusExposeOnlySafeState(t *testing.T) {
	identityKey := installMachineTestIdentity(t)
	dir := t.TempDir()
	saveMachineTestSession(t, dir, "busy")
	service := testCLISessionService(t, dir)
	binding, err := service.Open(t.Context(), session.SessionRef{HostID: "local", SessionID: "busy"})
	if err != nil {
		t.Fatalf("acquire session lease: %v", err)
	}
	defer binding.Release(t.Context())

	for _, operation := range []string{"show", "status"} {
		var out bytes.Buffer
		args := []string{operation, "--json", machineSessionIDWithKey("busy", identityKey), "--dir", dir}
		if code := runSessionCommand(args, &out); code != 0 {
			t.Fatalf("%s exit code = %d, output = %s", operation, code, out.String())
		}
		var response machineSessionShow
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatalf("decode %s: %v", operation, err)
		}
		if response.Command != "session."+operation || response.Session.State != "active" {
			t.Errorf("%s response = %+v", operation, response)
		}
		if strings.Contains(out.String(), dir) || strings.Contains(out.String(), "PRIVATE") {
			t.Fatalf("%s output leaked private data: %s", operation, out.String())
		}
	}
}

func TestMachineSessionIDIsStableAndOpaque(t *testing.T) {
	raw := "20260723-120000.000000000-private-provider-model"
	identityKey := bytes.Repeat([]byte{0x41}, machineIdentityKeyBytes)
	otherKey := bytes.Repeat([]byte{0x42}, machineIdentityKeyBytes)
	first := machineSessionIDWithKey(raw, identityKey)
	if first == "" || first != machineSessionIDWithKey(raw, identityKey) {
		t.Fatalf("machine session id is not stable: %q", first)
	}
	if strings.Contains(first, "private-provider-model") || first == raw {
		t.Fatalf("machine session id exposed raw branch identity: %q", first)
	}
	if first == machineSessionIDWithKey(raw+"-other", identityKey) {
		t.Fatalf("different branch ids collided: %q", first)
	}
	if first == machineSessionIDWithKey(raw, otherKey) {
		t.Fatalf("different installations produced the same machine id: %q", first)
	}
}

func TestSessionMachineErrorsAreJSONAndNonZero(t *testing.T) {
	installMachineTestIdentity(t)
	dir := t.TempDir()
	cases := []struct {
		name string
		args []string
		code int
		err  string
	}{
		{name: "missing json", args: []string{"list", "--dir", dir}, code: 2, err: "invalid_argument"},
		{name: "missing session", args: []string{"show", "--json", "missing", "--dir", dir}, code: 1, err: "session_not_found"},
		{name: "conflicting session sources", args: []string{"list", "--json", "--dir", dir, "--project-root", dir}, code: 2, err: "invalid_argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := runSessionCommand(tc.args, &out); code != tc.code {
				t.Fatalf("exit code = %d, want %d; output = %s", code, tc.code, out.String())
			}
			var response machineErrorResponse
			if err := json.Unmarshal(out.Bytes(), &response); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if response.SchemaVersion != machineSchemaVersion || response.Error.Code != tc.err {
				t.Fatalf("response = %+v", response)
			}
			if strings.Contains(out.String(), dir) {
				t.Fatalf("error output leaked path: %s", out.String())
			}
		})
	}
}

func saveMachineTestSession(t *testing.T, dir, id string) string {
	t.Helper()
	service := testCLISessionService(t, dir)
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id, CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"message": provider.Message{ID: id + "-user", Role: provider.RoleUser, Content: "PRIVATE prompt"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Append(t.Context(), session.Batch{OperationID: "seed", TurnID: "turn", Events: []session.Event{
		{Kind: "turn/start"}, {Kind: "message/complete", Payload: payload},
		{Kind: "turn/end", Payload: json.RawMessage(`{"status":"completed"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	path, err := service.SessionDir(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	return path
}
