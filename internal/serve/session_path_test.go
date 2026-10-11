package serve

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/control"
)

func TestResolveSessionPathRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unprivileged Windows CI cannot create the symlink fixture")
	}
	sessionDir := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.jsonl")
	if err := os.WriteFile(outside, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(sessionDir, "alias.jsonl")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}

	ctrl := control.New(control.Options{SessionDir: sessionDir})
	server := New(ctrl, NewBroadcaster(), config.ServeConfig{})
	t.Cleanup(server.Close)
	if _, err := server.resolveSessionPath(alias); err == nil || err.Error() != "path outside session dir" {
		t.Fatalf("resolve symlink escape error = %v, want path outside session dir", err)
	}
}

func TestResolveSessionPathContainment(t *testing.T) {
	dir := t.TempDir()
	physicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctrl := control.New(control.Options{SessionDir: dir})
	server := New(ctrl, NewBroadcaster(), config.ServeConfig{})
	t.Cleanup(server.Close)
	for _, path := range []string{filepath.Join(dir, "..", "outside.jsonl"), dir + "-sibling" + string(os.PathSeparator) + "outside.jsonl", dir, ""} {
		if got, err := server.resolveSessionPath(path); err == nil {
			t.Errorf("outside/non-transcript path accepted: %q -> %q", path, got)
		}
	}
	for _, rel := range []string{"inside.jsonl", "nested/inside.jsonl", "name..with-dots.jsonl"} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		got, err := server.resolveSessionPath(path)
		if err != nil || !strings.EqualFold(got, filepath.Join(physicalDir, filepath.FromSlash(rel))) {
			t.Errorf("valid contained path %q = %q, %v", path, got, err)
		}
	}
}

func TestResolveSessionPathAllowsContainedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unprivileged Windows CI cannot create the symlink fixture")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.jsonl")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.jsonl")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	physicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	server := New(control.New(control.Options{SessionDir: dir}), NewBroadcaster(), config.ServeConfig{})
	t.Cleanup(server.Close)
	if got, err := server.resolveSessionPath(alias); err != nil || got != physicalTarget {
		t.Fatalf("contained symlink = %q, %v; want %q", got, err, target)
	}
}

func TestResolveSessionPathPreservesCaseInsensitiveRootAlias(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "MiXeD-Root")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := strings.ToLower(dir)
	if _, err := os.Stat(alias); err != nil {
		t.Skip("fixture volume uses case-sensitive paths")
	}
	physicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := New(control.New(control.Options{SessionDir: dir}), NewBroadcaster(), config.ServeConfig{})
	t.Cleanup(server.Close)
	got, err := server.resolveSessionPath(filepath.Join(alias, "MiXeD-Session.jsonl"))
	if err != nil || got != filepath.Join(physicalDir, "MiXeD-Session.jsonl") {
		t.Fatalf("case-insensitive alias lost containment or access spelling: %q, %v", got, err)
	}
}
