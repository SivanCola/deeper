package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectStatePathsAreStableDistinctAndReadOnly(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	first := filepath.Join(root, "front-end", "app")
	second := filepath.Join(root, "front", "end-app")
	if WorkspaceSlug(first) != WorkspaceSlug(second) {
		t.Fatal("fixture must collide under retired slug format")
	}
	a, b := ProjectStateDir(state, first), ProjectStateDir(state, second)
	if a == b || a != ProjectStateDir(state, first) {
		t.Fatal("current workspace identities are unstable or collide")
	}
	if _, err := os.Stat(filepath.Join(state, "projects")); !os.IsNotExist(err) {
		t.Fatal("path lookup created state")
	}
}
