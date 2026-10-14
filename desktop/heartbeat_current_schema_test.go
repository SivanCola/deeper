package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestHeartbeatRejectsOldConfigWithoutOverwriting(t *testing.T) {
	for _, raw := range []string{
		`{"tasks":[{"id":"old"}]}`,
		`{"schemaVersion":1,"tasks":[{"id":"old"}]}`,
		`{"schemaVersion":2,"revision":1,"tasks":[{"id":"old","runHistory":[{"at":100}]}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			isolateDesktopUserDirs(t)
			e := &HeartbeatEngine{}
			if err := os.MkdirAll(filepath.Dir(e.configPath()), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(e.configPath(), []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := e.readConfigSnapshot(); err == nil {
				t.Fatal("old config accepted")
			}
			if err := e.ReplaceTasks([]HeartbeatTask{{ID: "new"}}); err == nil {
				t.Fatal("old config overwritten")
			}
			got, err := os.ReadFile(e.configPath())
			if err != nil || !bytes.Equal(got, []byte(raw)) {
				t.Fatalf("old config changed: %s, %v", got, err)
			}
		})
	}
}

func TestHeartbeatRejectsUnversionedRunHistory(t *testing.T) {
	isolateDesktopUserDirs(t)
	e := &HeartbeatEngine{}
	if err := e.ReplaceTasks([]HeartbeatTask{{ID: "current"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.runHistoryPath(), []byte(`{"runs":{"current":[{"at":100}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.readConfigSnapshot(); err == nil {
		t.Fatal("unversioned run history accepted")
	}
}
