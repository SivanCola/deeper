package main

import (
	"encoding/json"
	"path/filepath"
)

func loadProjectsFile() desktopProjectFile {
	return newDesktopPathMatcher().loadProjectsFile()
}

func (m desktopPathMatcher) loadProjectsFile() desktopProjectFile {
	path := filepath.Join(desktopConfigDir(), desktopProjectsFile)
	b, err := readFileUTF8(path)
	if err != nil {
		return desktopProjectFile{}
	}
	var f desktopProjectFile
	_ = json.Unmarshal(b, &f)
	f = m.normalizeProjectsFile(f)
	return f
}
