package main

import (
	"errors"
	"os"
	"strings"

	"deepzora/internal/config"
)

func desktopSessionDir(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return config.SessionDir()
		}
		root = cwd
	}
	if dir := config.ProjectSessionDir(root); dir != "" {
		return dir
	}
	return config.SessionDir()
}

// errSessionBusyElsewhere is the sanitized error surfaced when a destructive
// session operation is blocked by a live owner. It intentionally carries no
// writer id, hostname, or path.
var errSessionBusyElsewhere = errors.New("session is in use by another Deepzora window or process")
