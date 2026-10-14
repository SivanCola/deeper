package desktoplauncher

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestShortcutRepairUsesLauncherRootAndExactInstallerPaths(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(t.TempDir(), "Deepzora.lnk"), filepath.Join(t.TempDir(), "Deepzora.LNK")}
	called := false
	err := repairInstallerShortcuts(paths, func() (string, error) { return root, nil }, func(gotRoot string, gotPaths []string) error {
		called = true
		if gotRoot != root || !reflect.DeepEqual(gotPaths, paths) {
			t.Fatalf("repair = %q %v, want %q %v", gotRoot, gotPaths, root, paths)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("repair = %v, called=%t", err, called)
	}
}

func TestShortcutRepairRejectsInvalidArgumentsBeforeAnyWrite(t *testing.T) {
	root := t.TempDir()
	for _, paths := range [][]string{nil, {"relative.lnk"}, {filepath.Join(root, "Deepzora.exe")}, {filepath.Join(root, "good.lnk"), "bad.lnk"}} {
		err := repairInstallerShortcuts(paths, func() (string, error) { return root, nil }, func(string, []string) error {
			t.Fatal("invalid maintenance arguments reached the repair writer")
			return nil
		})
		if err == nil {
			t.Fatalf("accepted invalid paths %v", paths)
		}
	}
}

func TestShortcutRepairDoesNotFallThroughToDesktopLaunch(t *testing.T) {
	if code := Run([]string{"--repair-shortcuts"}, "test"); code != 1 {
		t.Fatalf("empty maintenance command exit = %d, want 1", code)
	}
	// Optional installer shortcuts may be absent. A successful maintenance
	// command must return without looking for or launching a desktop binary.
	path := filepath.Join(t.TempDir(), "Deepzora.lnk")
	if code := Run([]string{"--repair-shortcuts", path}, "test"); code != 0 {
		t.Fatalf("valid maintenance command exit = %d, want 0 without a desktop launch", code)
	}
}

func TestShortcutRepairPropagatesErrors(t *testing.T) {
	want := errors.New("access denied")
	path := filepath.Join(t.TempDir(), "Deepzora.lnk")
	err := repairInstallerShortcuts([]string{path}, func() (string, error) { return "", want }, func(string, []string) error {
		t.Fatal("root failure reached writer")
		return nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("root error = %v", err)
	}
	err = repairInstallerShortcuts([]string{path}, func() (string, error) { return t.TempDir(), nil }, func(string, []string) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("repair error = %v", err)
	}
}
