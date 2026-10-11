package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	filelock "deepzora/internal/identitylock"
	"deepzora/internal/topicstate"
)

const (
	topicStateLockTimeout      = 2 * time.Second
	topicStateOperationTimeout = 5 * time.Second
)

type topicStateManager struct {
	mu               sync.Mutex
	scopes           map[string]*topicStateScope
	now              func() time.Time
	operationContext func() (context.Context, context.CancelFunc)
}

type topicStateScope struct {
	mu    sync.Mutex
	root  string
	path  string
	store *topicstate.Store
}

var desktopTopicState = newTopicStateManager()

func newTopicStateManager() *topicStateManager {
	return &topicStateManager{
		scopes: map[string]*topicStateScope{}, now: time.Now,
		operationContext: newTopicOperationContext,
	}
}

func (m *topicStateManager) scope(workspaceRoot string) *topicStateScope {
	workspaceRoot = normalizeProjectRoot(workspaceRoot)
	path := config.DesktopTopicStatePath(workspaceRoot)
	key := path
	if key == "" {
		key = "missing:" + workspaceRoot
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if scope := m.scopes[key]; scope != nil {
		return scope
	}
	scope := &topicStateScope{root: workspaceRoot, path: path}
	m.scopes[key] = scope
	return scope
}

func (m *topicStateManager) close() {
	m.mu.Lock()
	scopes := make([]*topicStateScope, 0, len(m.scopes))
	for _, scope := range m.scopes {
		scopes = append(scopes, scope)
	}
	m.scopes = map[string]*topicStateScope{}
	m.mu.Unlock()
	for _, scope := range scopes {
		scope.mu.Lock()
		if scope.store != nil {
			_ = scope.store.Close()
			scope.store = nil
		}
		scope.mu.Unlock()
	}
}

func (m *topicStateManager) snapshot(workspaceRoot string) (topicstate.Snapshot, error) {
	scope := m.scope(workspaceRoot)
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if err := m.ensureOpenLocked(scope); err != nil {
		return topicstate.Snapshot{}, err
	}
	return scope.store.Snapshot(context.Background())
}

func (m *topicStateManager) mutate(workspaceRoot string, mutation func(context.Context, *topicstate.Store) (topicstate.State, error)) error {
	scope := m.scope(workspaceRoot)
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.root != "" && !existingDirectory(scope.root) {
		return fmt.Errorf("workspace root %q no longer exists", scope.root)
	}
	if scope.path == "" {
		return errors.New("topic state directory is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(scope.path), 0o700); err != nil {
		return err
	}
	lockCtx, cancelLock := context.WithTimeout(context.Background(), topicStateLockTimeout)
	release, err := filelock.Acquire(lockCtx, scope.path+".lock")
	cancelLock()
	if err != nil {
		return err
	}
	defer release()
	if err := m.ensureStoreOpenLocked(scope); err != nil {

		return err
	}
	// Opening/reconciling uses its own context. Start the mutation's budget only
	// after that phase; cold SQLite startup must not hand it an expired context.
	ctx, cancelOperation := m.operationContext()
	defer cancelOperation()
	if _, err := mutation(ctx, scope.store); err != nil {
		return err
	}

	return nil
}

func (m *topicStateManager) ensureOpenLocked(scope *topicStateScope) error {
	if scope.store != nil {
		return nil
	}
	if scope.path == "" {
		return errors.New("topic state directory is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(scope.path), 0o700); err != nil {
		return err
	}
	lockCtx, cancelLock := context.WithTimeout(context.Background(), topicStateLockTimeout)
	release, err := filelock.Acquire(lockCtx, scope.path+".lock")
	cancelLock()
	if err != nil {
		return err
	}
	defer release()
	return m.ensureStoreOpenLocked(scope)
}

func (m *topicStateManager) ensureStoreOpenLocked(scope *topicStateScope) error {
	if scope.store != nil {
		return nil
	}
	store, err := topicstate.Open(context.Background(), scope.path)
	if err != nil {
		return err
	}
	scope.store = store
	return nil
}

func (m *topicStateManager) setTitle(workspaceRoot, topicID, title, source string) error {
	topicID, title, source = strings.TrimSpace(topicID), strings.TrimSpace(title), strings.TrimSpace(source)
	return m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.Update(ctx, topicID, func(record *topicstate.Record) {
			applyTopicTitle(record, title, source)
		})
	})
}

func (m *topicStateManager) setCreatedAt(workspaceRoot, topicID string, createdAt int64) error {
	topicID = strings.TrimSpace(topicID)
	return m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.Update(ctx, topicID, func(record *topicstate.Record) { record.CreatedAtMS = createdAt })
	})
}

func (m *topicStateManager) setAutoMeta(workspaceRoot, topicID string, value *topicAutoTitleMeta) error {
	topicID = strings.TrimSpace(topicID)
	return m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.Update(ctx, topicID, func(record *topicstate.Record) {
			if value == nil {
				record.AutoMeta = nil
				return
			}
			record.AutoMeta = mergeKnownAutoMeta(record.AutoMeta, *value)
		})
	})
}

func (m *topicStateManager) delete(workspaceRoot, topicID string) error {
	topicID = strings.TrimSpace(topicID)
	return m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.Delete(ctx, topicID)
	})
}

func (m *topicStateManager) replaceTitles(workspaceRoot string, values map[string]string) error {
	return m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.ReplaceTitles(ctx, values)
	})
}

func (m *topicStateManager) replaceSources(workspaceRoot string, values map[string]string) error {
	return m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.ReplaceSources(ctx, values)
	})
}

func (m *topicStateManager) mergeMissingTitleIndex(workspaceRoot string, titles, sources map[string]string) error {
	return m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.MergeMissingTitleIndex(ctx, titles, sources, deletedTopicSet())
	})
}

func (m *topicStateManager) replaceCreatedAts(workspaceRoot string, values map[string]int64) error {
	return m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.ReplaceCreatedAts(ctx, values)
	})
}

func loadTopicTitles(workspaceRoot string) map[string]string {
	values := map[string]string{}
	snapshot, err := desktopTopicState.snapshot(workspaceRoot)

	if err != nil {
		return values
	}
	for id, record := range snapshot.Records {
		if record.Title != "" {
			values[id] = agent.UserPreviewText(record.Title)
		}
	}
	return values
}

func loadTopicTitleSources(workspaceRoot string) map[string]string {
	values := map[string]string{}
	snapshot, err := desktopTopicState.snapshot(workspaceRoot)

	if err != nil {
		return values
	}
	for id, record := range snapshot.Records {
		if record.TitleSource != "" {
			values[id] = record.TitleSource
		}
	}
	return values
}

func loadTopicCreatedAts(workspaceRoot string) map[string]int64 {
	values := map[string]int64{}
	snapshot, err := desktopTopicState.snapshot(workspaceRoot)

	if err != nil {
		return values
	}
	for id, record := range snapshot.Records {
		if record.CreatedAtMS > 0 {
			values[id] = record.CreatedAtMS
		}
	}
	return values
}

func loadTopicAutoTitleMeta(workspaceRoot string) map[string]topicAutoTitleMeta {
	values := map[string]topicAutoTitleMeta{}
	snapshot, err := desktopTopicState.snapshot(workspaceRoot)

	if err != nil {
		return values
	}
	for id, record := range snapshot.Records {
		var value topicAutoTitleMeta
		if len(record.AutoMeta) > 0 && json.Unmarshal(record.AutoMeta, &value) == nil {
			values[id] = value
		}
	}
	return values
}

func saveTopicTitleIndex(workspaceRoot string, titles, sources map[string]string) error {
	if titles == nil {
		titles = loadTopicTitles(workspaceRoot)
	}
	if sources == nil {
		sources = loadTopicTitleSources(workspaceRoot)
	}
	return desktopTopicState.mergeMissingTitleIndex(workspaceRoot, titles, sources)
}

func deletedTopicSet() map[string]bool {
	deleted := loadProjectsFile().DeletedTopics
	set := make(map[string]bool, len(deleted))
	for _, topicID := range deleted {
		set[strings.TrimSpace(topicID)] = true
	}
	return set
}

func mergeKnownAutoMeta(existing json.RawMessage, value topicAutoTitleMeta) json.RawMessage {
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(existing, &fields)
	known, _ := json.Marshal(value)
	var knownFields map[string]json.RawMessage
	_ = json.Unmarshal(known, &knownFields)
	for _, key := range []string{"stage", "userTurns", "basisHash", "updatedAt"} {
		delete(fields, key)
	}
	maps.Copy(fields, knownFields)
	data, _ := json.Marshal(fields)
	return data
}
