package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/session"
)

func isolateCLIConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Cleanup(closeTestSessionServices)
	t.Setenv("HOME", home)
	// Keep tests on the default-path code path while preventing a caller's
	// higher-priority DEEPZORA_HOME from escaping this temporary home.
	t.Setenv("DEEPZORA_HOME", "")
	if err := os.Unsetenv("DEEPZORA_HOME"); err != nil {
		t.Fatalf("unset DEEPZORA_HOME: %v", err)
	}
	t.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	t.Chdir(t.TempDir())
	return home
}

func closeTestSessionServices() {
	cliSessionServices.Lock()
	services := cliSessionServices.byRoot
	cliSessionServices.byRoot = map[string]*session.Service{}
	cliSessionServices.Unlock()
	for _, service := range services {
		_ = service.Shutdown(context.Background())
	}
}
