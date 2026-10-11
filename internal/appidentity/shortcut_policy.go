package appidentity

import (
	"os"
	"path/filepath"
	"strings"
)

type shortcutRepair struct {
	identity string
}

type shortcutTargetKind uint8

const (
	foreignShortcutTarget shortcutTargetKind = iota
	stableShortcutTarget
)

func planShortcutRepair(target, id, root string) shortcutRepair {
	if !ownedShortcutTarget(target, root) || id != "" {
		return shortcutRepair{}
	}
	return shortcutRepair{identity: AppUserModelID}
}

func ownedShortcutTarget(target, root string) bool {
	return classifyShortcutTarget(target, root) != foreignShortcutTarget
}

func classifyShortcutTarget(target, root string) shortcutTargetKind {
	if !filepath.IsAbs(target) || !filepath.IsAbs(root) {
		return foreignShortcutTarget
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return foreignShortcutTarget
	}
	root, err = existingShortcutPath(root)
	if err != nil {
		return foreignShortcutTarget
	}
	target, err = resolveShortcutTarget(target)
	if err != nil {
		return foreignShortcutTarget
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return foreignShortcutTarget
	}
	parts := strings.Split(strings.ToLower(rel), string(filepath.Separator))
	if len(parts) == 1 {
		switch parts[0] {
		case "deepzora.exe":
			return stableShortcutTarget
		}
	}
	return foreignShortcutTarget
}

// Resolve the existing ancestor even after an updater prunes a version. A
// junction inside versions must not make another installation look owned.
func resolveShortcutTarget(path string) (string, error) {
	path = filepath.Clean(path)
	if _, err := os.Lstat(path); err == nil {
		return existingShortcutPath(path)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parentPath := filepath.Dir(path)
	if parentPath == path {
		return "", &os.PathError{Op: "resolve shortcut", Path: path, Err: os.ErrNotExist}
	}
	parent, err := resolveShortcutTarget(parentPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
