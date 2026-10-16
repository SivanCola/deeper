package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/session"
)

func TestDoctorSourceResolvesCurrentStoresWithoutWriters(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	cwd := t.TempDir()
	t.Chdir(cwd)
	stores := map[string]string{
		"desktop": config.DesktopSessionStoreDir(),
		"global":  session.RootForSessionDir(config.SessionDir()),
		"project": session.RootForSessionDir(config.ProjectSessionDir(cwd)),
	}
	for id, root := range stores {
		createCanonicalTestSession(t, root, id, "current "+id)
	}
	for id, root := range stores {
		source, err := openDoctorSession("session-id:" + id)
		if err != nil {
			t.Fatal(err)
		}
		messages, err := source.service.Query().History(t.Context(), source.ref)
		if err != nil || len(messages) != 1 || messages[0].Content != "current "+id {
			t.Fatalf("wrong source %s: %+v %v", id, messages, err)
		}
		if _, live := source.service.Runtime(source.ref); live || session.ProbeWriterHeld(filepath.Join(root, id)) {
			t.Fatal("doctor opened a writer")
		}
		if (source.telemetryPath != "") != (id == "desktop") {
			t.Fatalf("wrong telemetry scope for %s: %q", id, source.telemetryPath)
		}
		if err := source.service.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDoctorSourceRejectsAmbiguousCurrentIdentity(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	createCanonicalTestSession(t, config.DesktopSessionStoreDir(), "same-id", "desktop")
	createCanonicalTestSession(t, session.RootForSessionDir(config.SessionDir()), "same-id", "cli")
	if _, err := openDoctorSession("same-id"); err == nil || !strings.Contains(err.Error(), "multiple current stores") {
		t.Fatalf("ambiguous identity chosen: %v", err)
	}
}

func TestDoctorCommandsRejectOldFileReferencesWithoutModification(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "old.jsonl")
	body := []byte(`{"role":"user","content":"old file"}` + "\n")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"old", path, path + ".meta"} {
		out := filepath.Join(t.TempDir(), "never.zip")
		captureStdout(t, func() {
			if rc := doctorQualityCommand([]string{ref, "--json"}, "test"); rc != 1 {
				t.Fatalf("quality accepted %q: %d", ref, rc)
			}
			if rc := doctorSessionCommand([]string{ref, "--out", out}, "test"); rc != 1 {
				t.Fatalf("bundle accepted %q: %d", ref, rc)
			}
		})
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("failed bundle created output: %v", err)
		}
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
		t.Fatalf("old data modified: %v", err)
	}
}
