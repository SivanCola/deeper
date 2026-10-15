package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/fileutil"
	filelock "deepzora/internal/identitylock"
)

// Current native session display sidecars and trash artifact operations.

const sessionDisplayFile = ".display.json"
const sessionPlannerDisplayFile = ".planner-display.json"

const (
	// Durable sidecar publication includes an fsync while holding the update
	// lock. Keep enough queue budget for a burst of in-process writers on
	// slower Windows disks, but fail external contention quickly so turn
	// completion and retry-queue handoff do not stall behind another process.
	sessionSidecarQueueTimeout        = 5 * time.Second
	sessionSidecarExternalLockTimeout = 750 * time.Millisecond
)

var (
	sessionPlannerDisplayExternalLockTimeout = sessionSidecarExternalLockTimeout
	sessionDisplayExternalLockTimeout        = sessionSidecarExternalLockTimeout
)

func sessionDisplayPath(dir string) string { return filepath.Join(dir, sessionDisplayFile) }

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

type sessionTrashArtifact struct {
	src  string
	name string
}

func sessionTelemetryPath(sessionPath string) string {
	if strings.TrimSpace(sessionPath) == "" {
		return ""
	}
	return sessionPath + ".telemetry.json"
}

// errSessionBusyElsewhere is the sanitized error surfaced when a destructive
// session operation is blocked by a live owner. It intentionally carries no
// writer id, hostname, or path.
var errSessionBusyElsewhere = errors.New("session is in use by another Deepzora window or process")

// acquireSessionRemovalGuard wraps agent.TryAcquireSessionRemovalGuard with
// the sanitized busy error. The guard holds the session's save and lease
// locks across the destructive operation and deletes the lock files
// atomically with the release — a one-shot busy probe followed by RemoveAll
// would let another process acquire the lease in between and then lose its
// freshly locked lease file, breaking cross-process mutual exclusion.
func acquireSessionRemovalGuard(sessionPath string) (*agent.SessionRemovalGuard, error) {
	guard, err := withSessionLeaseContentionRetry(func() (*agent.SessionRemovalGuard, error) {
		return agent.TryAcquireSessionRemovalGuard(sessionPath)
	})
	if err != nil {
		if errors.Is(err, agent.ErrSessionLeaseHeld) {
			return nil, errSessionBusyElsewhere
		}
		return nil, err
	}
	return guard, nil
}

func sessionOwnedArtifactPaths(sessionPath string) []string {
	key := filepath.Base(sessionPath)
	artifacts := sessionTrashArtifacts(sessionPath, key)
	paths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.src) != "" {
			paths = append(paths, artifact.src)
		}
	}
	return paths
}

func sessionFileHasConversationContent(sessionPath string) bool {
	if strings.TrimSpace(sessionPath) == "" || agent.IsCleanupPending(sessionPath) {
		return false
	}
	info, err := os.Stat(sessionPath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false
	}
	session, err := agent.LoadSession(sessionPath)
	if err != nil {
		return false
	}
	return session.HasContent()
}

func validateSessionPath(dir, sessionPath string) (string, string, error) {
	if strings.TrimSpace(sessionPath) == "" {
		return "", "", fmt.Errorf("empty session path")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	path := sessionPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(absDir, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	if !isLegacySessionTranscriptName(filepath.Base(absPath)) {
		return "", "", fmt.Errorf("not a session file: %s", sessionPath)
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("session path outside session dir: %s", sessionPath)
	}
	if info, err := os.Lstat(absPath); err == nil {
		if info.IsDir() {
			return "", "", fmt.Errorf("not a session file: %s", sessionPath)
		}
		realDir, dirErr := filepath.EvalSymlinks(absDir)
		if dirErr != nil {
			realDir = absDir
		}
		realPath, err := filepath.EvalSymlinks(absPath)
		if err != nil {
			return "", "", err
		}
		rel, err := filepath.Rel(realDir, realPath)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
			return "", "", fmt.Errorf("session path escapes session dir: %s", sessionPath)
		}
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	return absPath, filepath.Base(absPath), nil
}

type sessionDisplayMap map[string]map[string]string

type sessionPlannerDisplayMap map[string][]plannerDisplayTurn

type plannerDisplayTurn struct {
	TurnID   string           `json:"turnId,omitempty"`
	UserHash string           `json:"userHash"`
	Messages []HistoryMessage `json:"messages"`
}

var errCorruptSessionPlannerDisplay = errors.New("corrupt planner display sidecar")

// sessionPlannerDisplayUpdateAfterLoad is a subprocess-test seam. Production
// leaves it nil; tests use it to force two independent processes into the old
// stale read-modify-write window without relying on scheduler timing.
var sessionPlannerDisplayUpdateAfterLoad func()

func messageDisplayKey(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum[:])
}

func loadSessionDisplays(dir string) sessionDisplayMap {
	m := sessionDisplayMap{}
	b, err := readFileUTF8(sessionDisplayPath(dir))
	if err != nil {
		return m
	}
	_ = json.Unmarshal(b, &m)
	return m
}

func sessionPlannerDisplayPath(dir string) string {
	return filepath.Join(dir, sessionPlannerDisplayFile)
}

func loadSessionPlannerDisplays(dir string) sessionPlannerDisplayMap {
	m := sessionPlannerDisplayMap{}
	if strings.TrimSpace(dir) == "" {
		return m
	}
	b, err := readFileUTF8(sessionPlannerDisplayPath(dir))
	if err != nil {
		return m
	}
	_ = json.Unmarshal(b, &m)
	return m
}

func loadSessionPlannerDisplaysForUpdate(dir string) (sessionPlannerDisplayMap, error) {
	m := sessionPlannerDisplayMap{}
	b, err := readFileUTF8(sessionPlannerDisplayPath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return m, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%w: %w", errCorruptSessionPlannerDisplay, err)
	}
	if m == nil {
		m = sessionPlannerDisplayMap{}
	}
	return m, nil
}

func saveSessionPlannerDisplays(dir string, m sessionPlannerDisplayMap) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(sessionPlannerDisplayPath(dir), b, 0o600)
}

func saveOrRemoveSessionPlannerDisplays(dir string, m sessionPlannerDisplayMap) error {
	if len(m) == 0 {
		err := os.Remove(sessionPlannerDisplayPath(dir))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return saveSessionPlannerDisplays(dir, m)
}

func updateSessionPlannerDisplays(dir string, recoverCorrupt bool, mutate func(sessionPlannerDisplayMap) bool) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("planner display directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionSidecarQueueTimeout)
	defer cancel()
	release, err := filelock.AcquireWithExternalTimeout(ctx, sessionPlannerDisplayPath(dir)+".lock", sessionPlannerDisplayExternalLockTimeout)
	if err != nil {
		return fmt.Errorf("lock planner display sidecar: %w", err)
	}
	defer release()

	m, err := loadSessionPlannerDisplaysForUpdate(dir)
	if err != nil {
		if !recoverCorrupt || !errors.Is(err, errCorruptSessionPlannerDisplay) {
			return err
		}
		// A corrupt shared map cannot be edited safely. Destructive cleanup is
		// allowed to retire the unreadable sidecar so deleted-session display
		// data does not linger and later records can start from a valid map.
		if removeErr := os.Remove(sessionPlannerDisplayPath(dir)); removeErr != nil && !os.IsNotExist(removeErr) {
			return errors.Join(err, removeErr)
		}
		m = sessionPlannerDisplayMap{}
	}
	if sessionPlannerDisplayUpdateAfterLoad != nil {
		sessionPlannerDisplayUpdateAfterLoad()
	}
	if !mutate(m) {
		return nil
	}
	return saveOrRemoveSessionPlannerDisplays(dir, m)
}

func recordSessionPlannerDisplayForTurn(dir, sessionPath, turnID, userContent string, messages []HistoryMessage) error {
	if strings.TrimSpace(sessionPath) == "" || strings.TrimSpace(userContent) == "" || len(messages) == 0 {
		return nil
	}
	key := filepath.Base(sessionPath)
	turn := plannerDisplayTurn{
		TurnID:   strings.TrimSpace(turnID),
		UserHash: messageDisplayKey(userContent),
		Messages: cloneHistoryMessages(messages),
	}
	return updateSessionPlannerDisplays(dir, false, func(m sessionPlannerDisplayMap) bool {
		if turn.TurnID != "" {
			for i := range m[key] {
				if m[key][i].TurnID == turn.TurnID {
					m[key][i] = turn
					return true
				}
			}
		}
		m[key] = append(m[key], turn)
		return true
	})
}

func removeSessionPlannerDisplay(dir, sessionPath string) error {
	if strings.TrimSpace(sessionPath) == "" {
		return nil
	}
	key := filepath.Base(sessionPath)
	return updateSessionPlannerDisplays(dir, true, func(m sessionPlannerDisplayMap) bool {
		if _, ok := m[key]; !ok {
			return false
		}
		delete(m, key)
		return true
	})
}

func sessionPlannerDisplayTurns(dir, sessionPath string) []plannerDisplayTurn {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(sessionPath) == "" {
		return nil
	}
	turns := loadSessionPlannerDisplays(dir)[filepath.Base(sessionPath)]
	if len(turns) == 0 {
		return nil
	}
	out := make([]plannerDisplayTurn, 0, len(turns))
	for _, turn := range turns {
		if strings.TrimSpace(turn.UserHash) == "" || len(turn.Messages) == 0 {
			continue
		}
		out = append(out, plannerDisplayTurn{
			TurnID:   turn.TurnID,
			UserHash: turn.UserHash,
			Messages: cloneHistoryMessages(turn.Messages),
		})
	}
	return out
}

func saveSessionDisplays(dir string, m sessionDisplayMap) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(sessionDisplayPath(dir), b, 0o600)
}

func saveOrRemoveSessionDisplays(dir string, m sessionDisplayMap) error {
	if len(m) == 0 {
		err := os.Remove(sessionDisplayPath(dir))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return saveSessionDisplays(dir, m)
}

// updateSessionDisplays serializes the display sidecar's read-modify-write
// cycle. Parallel tabs can record display text concurrently; atomic rename
// protects readers from partial JSON but cannot prevent the last writer from
// replacing another tab's freshly added keys (#6873).
func updateSessionDisplays(dir string, mutate func(sessionDisplayMap) bool) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("display directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionSidecarQueueTimeout)
	defer cancel()
	release, err := filelock.AcquireWithExternalTimeout(ctx, sessionDisplayPath(dir)+".lock", sessionDisplayExternalLockTimeout)
	if err != nil {
		return fmt.Errorf("lock display sidecar: %w", err)
	}
	defer release()

	m := loadSessionDisplays(dir)
	if !mutate(m) {
		return nil
	}
	return saveOrRemoveSessionDisplays(dir, m)
}

func removeSessionDisplayKey(dir, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	return updateSessionDisplays(dir, func(m sessionDisplayMap) bool {
		if m[key] == nil {
			return false
		}
		delete(m, key)
		return true
	})
}

func removeSessionDisplay(dir, sessionPath string) error {
	if strings.TrimSpace(sessionPath) == "" {
		return nil
	}
	return removeSessionDisplayKey(dir, filepath.Base(sessionPath))
}

func recordSessionDisplay(dir, sessionPath, content, display string) error {
	if strings.TrimSpace(sessionPath) == "" || content == display || strings.TrimSpace(display) == "" {
		return nil
	}
	return updateSessionDisplays(dir, func(m sessionDisplayMap) bool {
		key := filepath.Base(sessionPath)
		if m[key] == nil {
			m[key] = map[string]string{}
		}
		m[key][messageDisplayKey(content)] = display
		return true
	})
}

// sessionDisplayResolver loads the sidecar once and returns a per-message
// resolver, so a transcript of N messages doesn't re-read .display.json N times.
func sessionDisplayResolver(dir, sessionPath string) func(content string) string {
	return sessionDisplayResolverFromMap(loadSessionDisplays(dir), sessionPath)
}

func sessionDisplayResolverFromMap(displays sessionDisplayMap, sessionPath string) func(content string) string {
	byHash := displays[filepath.Base(sessionPath)]
	return func(content string) string {
		if byHash != nil {
			if display := byHash[messageDisplayKey(content)]; strings.TrimSpace(display) != "" {
				return display
			}
		}
		return historyReplayUserContent(content)
	}
}
