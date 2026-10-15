package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/pathidentity"
)

// Every sidebar read loads the projects file, and on Windows one resolution
// opens every component of the path, so the load must resolve each root a
// bounded number of times rather than once per pair of roots.
func TestLoadProjectsFileResolvesEachRootLinearly(t *testing.T) {
	isolateDesktopUserDirs(t)
	base := t.TempDir()
	const projects = 24
	var file desktopProjectFile
	for i := range projects {
		root := filepath.Join(base, fmt.Sprintf("project-%02d", i))
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		file.Projects = append(file.Projects, desktopProject{Root: root, Topics: []string{"t"}})
		file.SidebarOrder = append(file.SidebarOrder, root)
		if i%3 == 0 {
			file.PinnedProjects = append(file.PinnedProjects, root)
		}
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(file.Projects[5].Root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	file.Projects = append(file.Projects, desktopProject{Root: alias, Title: "Alias title"})
	if err := saveProjectsFile(file); err != nil {
		t.Fatal(err)
	}
	// Keep the alias in the input so the load
	// must fold it while resolving each distinct path only once.
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktopConfigDir(), desktopProjectsFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		read func(desktopPathMatcher) (desktopProjectFile, error)
	}{
		{"sidebar", func(m desktopPathMatcher) (desktopProjectFile, error) { return m.loadProjectsFile(), nil }},
		{"removal", desktopPathMatcher.readTopicRemovalProjects},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertProjectFileReadIdentityScope(t, projects, test.read)
		})
	}
}

func assertProjectFileReadIdentityScope(t *testing.T, projects int, read func(desktopPathMatcher) (desktopProjectFile, error)) {
	t.Helper()
	resolutions := map[string]int{}
	matcher := desktopPathMatcher{pathidentity.NewMatcher(pathidentity.Options{FollowLeaf: true}, func(path string, options pathidentity.Options) (pathidentity.Identity, error) {
		resolutions[path]++
		if len(resolutions) == 1 && resolutions[path] == 1 {
			// Force another read to complete while this read's resolver is active.
			// Its probes must stay outside this operation's instrumentation.
			background := make(chan desktopProjectFile, 1)
			go func() { background <- loadProjectsFile() }()
			if other := <-background; len(other.Projects) != projects {
				t.Fatalf("independent read loaded %d projects, want %d", len(other.Projects), projects)
			}
		}
		return pathidentity.Resolve(path, options)
	})}

	loaded, err := read(matcher)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Projects) != projects {
		t.Fatalf("projects = %d, want %d with the alias folded into its target", len(loaded.Projects), projects)
	}
	if loaded.Projects[5].Title != "Alias title" {
		t.Fatalf("alias metadata was not merged into its target: %+v", loaded.Projects[5])
	}
	if len(loaded.SidebarOrder) != projects || len(loaded.PinnedProjects) != (projects+2)/3 {
		t.Fatalf("sidebar order %d / pinned %d changed", len(loaded.SidebarOrder), len(loaded.PinnedProjects))
	}
	if len(resolutions) != projects+1 {
		t.Fatalf("loading %d projects resolved %d paths, want %d including the alias", projects, len(resolutions), projects+1)
	}
	for path, count := range resolutions {
		if count != 1 {
			t.Errorf("path %s resolved %d times in one read, want 1", path, count)
		}
	}
}
