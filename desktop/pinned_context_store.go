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

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/fileutil"
	"deepzora/internal/store"
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
		SessionID:     agent.BranchID(sessionPath),
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
	path := store.SessionPinnedContext(sessionPath)
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
	wantID := agent.BranchID(sessionPath)
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
	return fileutil.AtomicWriteFileStrict(store.SessionPinnedContext(sessionPath), raw, 0o600)
}

func copyPinnedContextState(sourcePath, targetPath string) error {
	if strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(targetPath) == "" {
		return nil
	}
	if _, err := os.Stat(store.SessionPinnedContext(sourcePath)); errors.Is(err, os.ErrNotExist) {
		return savePinnedContextState(targetPath, []string{})
	} else if err != nil {
		return err
	}
	state, err := loadPinnedContextState(sourcePath)
	if err != nil {
		return err
	}
	return savePinnedContextState(targetPath, state.Files)
}

func loadPinnedContextStateOrEmpty(sessionPath, logMessage string) pinnedContextState {
	state, err := loadPinnedContextState(sessionPath)
	if err == nil {
		return state
	}
	slog.Warn(logMessage, "session", agent.BranchID(sessionPath), "err", err)
	return emptyPinnedContextState(sessionPath)
}

func prepareStartupPinnedContext(tab *WorkspaceTab, startupPath, persistedPath string) {
	if id, ok := parseSessionRoute(startupPath); ok {
		tab.setPinnedFiles(loadPinnedContextStateOrEmpty(desktopPinnedContextPath(id), "desktop: load pinned context").Files)
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
	return filepath.Join(config.DesktopSessionStoreDir(), id, id+".jsonl")
}

func pinnedContextStateForSessionBinding(tab *WorkspaceTab, sessionPath string) pinnedContextState {
	if id, ok := parseSessionRoute(sessionPath); ok {
		sessionPath = desktopPinnedContextPath(id)
	}
	return loadPinnedContextStateOrEmpty(sessionPath, "desktop: load session pinned context")
}

func applyPinnedContextSessionBinding(tab *WorkspaceTab, state pinnedContextState) {
	tab.setPinnedFiles(state.Files)
}
