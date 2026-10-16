package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"deepzora/internal/config"
	"deepzora/internal/fileutil"
)

const (
	pinnedContextSchemaVersion = 1
	maxPinnedContextStateBytes = 64 * 1024
)

type pinnedContextState struct {
	SchemaVersion int      `json:"schemaVersion"`
	SessionID     string   `json:"sessionId"`
	Files         []string `json:"files"`
}

func emptyPinnedContextState(sessionPath string) pinnedContextState {
	return pinnedContextState{
		SchemaVersion: pinnedContextSchemaVersion,
		SessionID:     filepath.Base(filepath.Dir(sessionPath)),
		Files:         []string{},
	}
}

func normalizePinnedContextFiles(files []string) ([]string, error) {
	out := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, path := range files {
		clean, err := normalizePinnedRelPath(path)
		if err != nil {
			return nil, fmt.Errorf("invalid pinned path %q: %w", path, err)
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	if len(out) > maxPinnedFileCount {
		return nil, fmt.Errorf("at most %d files can be pinned", maxPinnedFileCount)
	}
	sort.Strings(out)
	return out, nil
}

func loadPinnedContextState(sessionPath string) (pinnedContextState, error) {
	sessionPath = strings.TrimSpace(sessionPath)
	state := emptyPinnedContextState(sessionPath)
	if sessionPath == "" {
		return state, nil
	}
	path := sessionPath
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read pinned context state: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return state, fmt.Errorf("stat pinned context state: %w", err)
	}
	if info.Size() > maxPinnedContextStateBytes {
		return state, fmt.Errorf("pinned context state exceeds %d bytes", maxPinnedContextStateBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxPinnedContextStateBytes+1))
	if err != nil {
		return state, fmt.Errorf("read pinned context state: %w", err)
	}
	if len(raw) > maxPinnedContextStateBytes {
		return state, fmt.Errorf("pinned context state exceeds %d bytes", maxPinnedContextStateBytes)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return emptyPinnedContextState(sessionPath), fmt.Errorf("decode pinned context state: %w", err)
	}
	if state.SchemaVersion != pinnedContextSchemaVersion {
		return emptyPinnedContextState(sessionPath), fmt.Errorf("unsupported pinned context schema version %d", state.SchemaVersion)
	}
	wantID := filepath.Base(filepath.Dir(sessionPath))
	if state.SessionID != wantID {
		return emptyPinnedContextState(sessionPath), fmt.Errorf("pinned context belongs to session %q, not %q", state.SessionID, wantID)
	}
	files, err := normalizePinnedContextFiles(state.Files)
	if err != nil {
		return emptyPinnedContextState(sessionPath), err
	}
	state.Files = files
	return state, nil
}

func savePinnedContextState(sessionPath string, files []string) error {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return fmt.Errorf("session is not ready")
	}
	normalized, err := normalizePinnedContextFiles(files)
	if err != nil {
		return err
	}
	state := emptyPinnedContextState(sessionPath)
	state.Files = normalized
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxPinnedContextStateBytes {
		return fmt.Errorf("pinned context state exceeds %d bytes", maxPinnedContextStateBytes)
	}
	return fileutil.AtomicWriteFileStrict(sessionPath, raw, 0o600)
}

func loadPinnedContextStateOrEmpty(sessionPath, logMessage string) pinnedContextState {
	state, err := loadPinnedContextState(sessionPath)
	if err == nil {
		return state
	}
	slog.Warn(logMessage, "session", filepath.Base(filepath.Dir(sessionPath)), "err", err)
	return emptyPinnedContextState(sessionPath)
}

func prepareStartupPinnedContext(tab *WorkspaceTab, sessionID string) {
	if sessionID != "" {
		tab.setPinnedFiles(loadPinnedContextStateOrEmpty(desktopPinnedContextPath(sessionID), "desktop: load pinned context").Files)
		return
	}
	tab.setPinnedFiles(nil)
}

func restoreTabPinnedContext(tab *WorkspaceTab) {
	if id, ok := parseSessionRoute(sessionRoute(tab.SessionID)); ok {
		tab.setPinnedFiles(loadPinnedContextStateOrEmpty(desktopPinnedContextPath(id), "desktop: restore pinned context").Files)
		return
	}
	tab.setPinnedFiles(nil)
}

func desktopPinnedContextPath(id string) string {
	return filepath.Join(config.DesktopSessionStoreDir(), id, "pinned-context.json")
}
