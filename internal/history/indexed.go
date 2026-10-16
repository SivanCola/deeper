package history

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"deepzora/internal/agent"
	"deepzora/internal/historycatalog"
	"deepzora/internal/provider"
)

type indexedCatalogManager struct {
	lifecycleMu sync.Mutex
	mu          sync.RWMutex
	catalog     *historycatalog.Catalog
	roots       map[string]historycatalog.Root
	observers   []func(historycatalog.Status, []string, string)
	generation  uint64
	opening     map[uint64]chan struct{}
	openCancel  map[uint64]context.CancelFunc
	closing     bool
	open        func(context.Context, historycatalog.Options) (*historycatalog.Catalog, error)
	rebuild     func(context.Context, historycatalog.Options, []historycatalog.Root) (historycatalog.Status, error)
}

var processHistoryCatalog indexedCatalogManager

// RegisterCatalogRoots lets a host such as Desktop seed every saved project
// without constructing a controller. Opening and scanning remain asynchronous.
func RegisterCatalogRoots(roots []historycatalog.Root) { processHistoryCatalog.register(roots) }

// RegisterCatalogObserver subscribes a host to revision/progress changes. The
// callback payload contains roots and counters only, never query or content.
func RegisterCatalogObserver(observer func(historycatalog.Status, []string, string)) {
	if observer == nil {
		return
	}
	processHistoryCatalog.mu.Lock()
	processHistoryCatalog.observers = append(processHistoryCatalog.observers, observer)
	processHistoryCatalog.mu.Unlock()
}

// SharedCatalog returns the process projection when opening has completed.
// Nil means callers should return an explicit partial/opening response.
func SharedCatalog() *historycatalog.Catalog { return processHistoryCatalog.get() }

func FlushSharedCatalog(ctx context.Context) error {
	if catalog := processHistoryCatalog.get(); catalog != nil {
		return catalog.Flush(ctx)
	}
	return nil
}

// CloseSharedCatalog cancels work and closes the process history catalog.
// Callers with time to persist queued projection work may invoke
// FlushSharedCatalog first. Close itself must not drain a potentially large
// backlog because current session events remain authoritative.
func CloseSharedCatalog(ctx context.Context) error {
	return processHistoryCatalog.close(ctx)
}

func (m *indexedCatalogManager) register(roots []historycatalog.Root) {
	m.mu.Lock()
	if m.roots == nil {
		m.roots = map[string]historycatalog.Root{}
	}
	for _, root := range roots {
		if strings.TrimSpace(root.Path) != "" {
			root.Path = filepath.Clean(root.Path)
			m.roots[root.Path] = root
		}
	}
	catalog := m.catalog
	if catalog == nil {
		m.startOpenLocked()
	}
	m.mu.Unlock()
	if catalog != nil {
		for _, root := range roots {
			catalog.RegisterRoot(root)
		}
	}
}

func (m *indexedCatalogManager) startOpenLocked() {
	if m.catalog != nil || m.closing || len(m.opening) != 0 {
		return
	}
	m.generation++
	generation := m.generation
	if m.opening == nil {
		m.opening = map[uint64]chan struct{}{}
		m.openCancel = map[uint64]context.CancelFunc{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.opening[generation] = done
	m.openCancel[generation] = cancel
	openCatalog := m.open
	if openCatalog == nil {
		openCatalog = historycatalog.Open
	}
	go m.openGeneration(ctx, generation, done, openCatalog)
}

func (m *indexedCatalogManager) openGeneration(ctx context.Context, generation uint64, done chan struct{}, openCatalog func(context.Context, historycatalog.Options) (*historycatalog.Catalog, error)) {
	catalog, err := openCatalog(ctx, historycatalog.Options{OnRevision: m.publish})
	if err == nil {
		seen := map[string]bool{}
		for {
			m.mu.Lock()
			if m.closing || generation != m.generation || ctx.Err() != nil {
				m.mu.Unlock()
				_ = catalog.Close(context.Background())
				catalog = nil
				break
			}
			pending := make([]historycatalog.Root, 0, len(m.roots))
			for path, root := range m.roots {
				if !seen[path] {
					seen[path] = true
					pending = append(pending, root)
				}
			}
			if len(pending) == 0 {
				m.catalog = catalog
				m.mu.Unlock()
				break
			}
			m.mu.Unlock()
			for _, root := range pending {
				catalog.RegisterRoot(root)
			}
		}
	}
	m.mu.Lock()
	delete(m.opening, generation)
	delete(m.openCancel, generation)
	close(done)
	m.mu.Unlock()
}

func (m *indexedCatalogManager) close(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	m.closing = true
	m.generation++
	catalog := m.catalog
	m.catalog = nil
	m.roots = nil
	m.observers = nil
	done := make([]chan struct{}, 0, len(m.opening))
	for generation, opening := range m.opening {
		m.openCancel[generation]()
		done = append(done, opening)
	}
	m.mu.Unlock()

	var closeErr error
	if catalog != nil {
		closeErr = catalog.Close(ctx)
	}
	for _, opening := range done {
		select {
		case <-opening:
		case <-ctx.Done():
			if closeErr == nil {
				closeErr = ctx.Err()
			}
		}
	}
	m.mu.Lock()
	m.closing = false
	m.mu.Unlock()
	return closeErr
}

func (m *indexedCatalogManager) publish(status historycatalog.Status, roots []string, reason string) {
	m.mu.RLock()
	observers := append([]func(historycatalog.Status, []string, string){}, m.observers...)
	m.mu.RUnlock()
	for _, observer := range observers {
		observer(status, append([]string{}, roots...), reason)
	}
}

func (m *indexedCatalogManager) get() *historycatalog.Catalog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.catalog
}

func candidateText(messages []provider.Message, candidate historycatalog.Candidate) (string, bool) {
	if candidate.MessageIndex < 0 || candidate.MessageIndex >= len(messages) {
		return "", false
	}
	msg := messages[candidate.MessageIndex]
	switch Kind(candidate.Kind) {
	case KindUserText:
		return stripComposePrefixes(msg.Content), msg.Role == provider.RoleUser && !agent.IsPinnedContextRevision(msg)
	case KindAssistantText:
		return msg.Content, msg.Role == provider.RoleAssistant
	case KindToolInput:
		if candidate.PartIndex < 0 || candidate.PartIndex >= len(msg.ToolCalls) {
			return "", false
		}
		call := msg.ToolCalls[candidate.PartIndex]
		return strings.TrimSpace(call.Name + " " + call.Arguments), true
	case KindToolError, KindToolOutput:
		return strings.TrimSpace(msg.Name + " " + msg.Content), msg.Role == provider.RoleTool
	default:
		return "", false
	}
}
