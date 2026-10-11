package config

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

func projectStateRoot(workspaceRoot string) string {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if runtimeGOOS == "windows" {
		root = strings.ToLower(root)
	}
	return root
}

func collisionProjectDir(userDir, root string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(userDir, "projects", "@"+hex.EncodeToString(sum[:]))
}

// ProjectStateDir uses a path hash so distinct workspace roots never share state.
func ProjectStateDir(userDir, workspaceRoot string) string {
	root := projectStateRoot(workspaceRoot)
	if userDir == "" || root == "" {
		return ""
	}
	return collisionProjectDir(userDir, root)
}
