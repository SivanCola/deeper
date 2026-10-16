package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"deepzora/internal/config"
	"deepzora/internal/pathidentity"
	"deepzora/internal/session"
)

type sessionCatalogTarget struct {
	Path          string
	Scope         string
	WorkspaceRoot string
}

func defaultSessionCatalogTargets() []sessionCatalogTarget {
	var saved struct {
		Projects []struct {
			Root string `json:"root"`
		} `json:"projects"`
	}
	home := config.DeepzoraHomeDir()
	if data, err := os.ReadFile(filepath.Join(home, "desktop-projects.json")); err == nil {
		_ = json.Unmarshal(data, &saved)
	}
	targets := []sessionCatalogTarget{
		{Path: config.SessionDir(), Scope: "global"},
		{Path: config.ProjectSessionDir(filepath.Join(home, "global-workspace")), Scope: "global"},
	}
	for _, project := range saved.Projects {
		if root := strings.TrimSpace(project.Root); root != "" {
			targets = append(targets, sessionCatalogTarget{Path: config.ProjectSessionDir(root), Scope: "project", WorkspaceRoot: root})
		}
	}
	return uniqueSessionCatalogTargets(targets)
}

func uniqueSessionCatalogTargets(targets []sessionCatalogTarget) []sessionCatalogTarget {
	seen := map[string]bool{}
	var out []sessionCatalogTarget
	for _, target := range targets {
		if strings.TrimSpace(target.Path) == "" {
			continue
		}
		absolute, err := filepath.Abs(target.Path)
		if err != nil {
			continue
		}
		key := absolute
		if identity, err := pathidentity.Resolve(absolute, pathidentity.Options{FollowLeaf: true}); err == nil {
			key = identity.Key
		}
		if !seen[key] {
			seen[key] = true
			target.Path = filepath.Clean(target.Path)
			out = append(out, target)
		}
	}
	return out
}

func currentSessionCatalogRoots(dirs []string) []string {
	var targets []sessionCatalogTarget
	if len(dirs) == 0 {
		for _, target := range defaultSessionCatalogTargets() {
			targets = append(targets, sessionCatalogTarget{Path: session.RootForSessionDir(target.Path)})
		}
		targets = append(targets, sessionCatalogTarget{Path: config.DesktopSessionStoreDir()})
	} else {
		for _, dir := range dirs {
			targets = append(targets, sessionCatalogTarget{Path: session.RootForSessionDir(dir)})
		}
	}
	var roots []string
	for _, target := range uniqueSessionCatalogTargets(targets) {
		roots = append(roots, target.Path)
	}
	return roots
}
