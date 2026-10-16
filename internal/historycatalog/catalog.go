package historycatalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"deepzora/internal/projectiondb"
	"deepzora/internal/provider"
)

const defaultMissingGrace = 30 * time.Second

type Catalog struct {
	// ReconcileRoot is also called synchronously by rebuild/inspection owners.
	// Keep its discovery generation and all source mutations in one writer order.
	indexMu    sync.Mutex
	db         *sql.DB
	opts       Options
	revision   atomic.Uint64
	statusMu   sync.RWMutex
	status     Status
	ctx        context.Context
	cancel     context.CancelFunc
	queue      chan string
	rootCh     chan string
	flushCh    chan chan struct{}
	mu         sync.Mutex
	paths      map[string]Root
	roots      map[string]Root
	dirtyRoots map[string]bool
	wg         sync.WaitGroup
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
}

func Open(ctx context.Context, opts Options) (*Catalog, error) {
	if opts.Path == "" {
		opts.Path = DefaultPath()
	}
	if strings.TrimSpace(opts.Path) == "" {
		opts.Path = ""
		opts.InMemory = true
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.QueueCapacity <= 0 {
		opts.QueueCapacity = 1024
	}
	if opts.MissingGrace <= 0 {
		opts.MissingGrace = defaultMissingGrace
	}
	if opts.ReconcileInterval <= 0 {
		// Periodic root rescans are fingerprint-cheap when nothing changed; keep
		// the interval longer so large installs are not re-walked every minute.
		opts.ReconcileInterval = 5 * time.Minute
	}
	opts.MaxBytes = resolveMaxBytes(opts.MaxBytes, configuredMaxMB())
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{
		Path: opts.Path, MemoryName: "history-search", Migrations: migrations(), InMemory: opts.InMemory,
		MaxOpenConns: 4, Now: opts.Now, SecureDelete: true, AutoVacuum: true,
	})
	if err != nil {
		return nil, err
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	c := &Catalog{db: handle.DB, opts: opts, ctx: workerCtx, cancel: cancel,
		queue: make(chan string, opts.QueueCapacity), rootCh: make(chan string, 64),
		flushCh: make(chan chan struct{}, 1),
		paths:   map[string]Root{}, roots: map[string]Root{}, dirtyRoots: map[string]bool{},
		closeDone: make(chan struct{}),
		status: Status{State: string(handle.Status.State), Mode: handle.Status.Mode, Path: handle.Status.Path,
			LastError: handle.Status.LastError, QuarantinedPath: handle.Status.QuarantinedPath}}
	if err := c.db.QueryRowContext(ctx, `SELECT revision FROM history_state WHERE id=1`).Scan(new(uint64)); err != nil {
		_ = c.db.Close()
		cancel()
		return nil, err
	}
	if err := c.ensureTokenizerVersion(ctx); err != nil {
		_ = c.db.Close()
		cancel()
		return nil, err
	}
	var revision uint64
	_ = c.db.QueryRowContext(ctx, `SELECT revision FROM history_state WHERE id=1`).Scan(&revision)
	c.revision.Store(revision)
	c.refreshStatus(ctx)
	c.wg.Add(1)
	go c.worker()
	// A far-over-cap index from before the cap existed (#8717) is cheaper to
	// rebuild than to evict session-by-session; wipe async so startup never
	// blocks. Registered roots rescan afterwards and re-index truncated.
	if !opts.InMemory && strings.TrimSpace(opts.Path) != "" &&
		historyDBFileSize(opts.Path) > rebuildOversizeFactor*opts.MaxBytes {
		c.wg.Go(func() {
			c.wipeForRebuild(c.ctx)
		})
	}
	return c, nil
}

func (c *Catalog) ensureTokenizerVersion(ctx context.Context) error {
	var version int
	if err := c.db.QueryRowContext(ctx, `SELECT tokenizer_version FROM history_state WHERE id=1`).Scan(&version); err != nil {
		return err
	}
	if version == TokenizerVersion {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := wipeProjectionRows(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE history_state SET tokenizer_version=?,revision=revision+1 WHERE id=1`, TokenizerVersion); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *Catalog) RegisterRoot(root Root) bool {
	if c == nil || root.validate() != nil {
		return false
	}
	root.Path = filepath.Clean(root.Path)
	if root.Scope != "project" {
		root.Scope = "global"
		root.WorkspaceRoot = ""
	}
	c.mu.Lock()
	_, alreadyRegistered := c.roots[root.Path]
	c.roots[root.Path] = root
	c.mu.Unlock()
	if !alreadyRegistered {
		c.statusMu.Lock()
		c.status.Pending++
		c.statusMu.Unlock()
	}
	select {
	case c.rootCh <- root.Path:
		return true
	default:
		c.markRootDirty(root.Path)
		return false
	}
}

// ReconcileRoot performs one deterministic scan. Production callers normally
// use RegisterRoot; the synchronous form exists for doctor/reindex and tests.
func (c *Catalog) ReconcileRoot(ctx context.Context, root Root) error {
	root.Path = filepath.Clean(root.Path)
	if root.Scope != "project" {
		root.Scope = "global"
		root.WorkspaceRoot = ""
	}
	return c.reconcileRoot(ctx, root)
}

func (c *Catalog) EnqueuePath(root Root, path string) bool {
	if c == nil || root.validate() != nil || strings.TrimSpace(path) == "" {
		return false
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) != root.Path {
		return false
	}
	c.mu.Lock()
	_, queued := c.paths[path]
	c.paths[path] = root
	c.mu.Unlock()
	if queued {
		return true
	}
	select {
	case c.queue <- path:
		return true
	default:
		c.mu.Lock()
		delete(c.paths, path)
		c.dirtyRoots[root.Path] = true
		c.mu.Unlock()
		return false
	}
}

// EnqueueExisting prioritizes a source already known to the catalog without
// requiring the caller to retain its root metadata.
func (c *Catalog) EnqueueExisting(ctx context.Context, path string) bool {
	var root Root
	err := c.db.QueryRowContext(ctx, `SELECT root,source,scope,workspace_root FROM history_sources WHERE path=?`, filepath.Clean(path)).Scan(
		&root.Path, &root.Source, &root.Scope, &root.WorkspaceRoot)
	if err != nil {
		return false
	}
	c.mu.Lock()
	registered, ok := c.roots[root.Path]
	c.mu.Unlock()
	return ok && c.EnqueuePath(registered, path)
}

func (c *Catalog) worker() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.opts.ReconcileInterval)
	defer ticker.Stop()
	for {
		if root, ok := c.takeDirtyRoot(); ok {
			_ = c.reconcileRoot(c.ctx, root)
			continue
		}
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.markAllRootsDirty()
			c.governSize(c.ctx)
		case done := <-c.flushCh:
			c.drainPending(c.ctx)
			close(done)
		case path := <-c.queue:
			c.mu.Lock()
			queued := c.paths[path]
			delete(c.paths, path)
			c.mu.Unlock()
			_ = c.indexQueuedSession(c.ctx, queued, path)
		case path := <-c.rootCh:
			c.mu.Lock()
			root, ok := c.roots[path]
			c.mu.Unlock()
			if ok {
				_ = c.reconcileRoot(c.ctx, root)
			}
		}
	}
}

// Flush drains dirty roots and the path queue until empty or ctx cancels, then
// waits for the worker to acknowledge. Callers use this on shutdown so pending
// index work is not silently abandoned.
func (c *Catalog) Flush(ctx context.Context) error {
	if c == nil {
		return nil
	}
	done := make(chan struct{})
	select {
	case c.flushCh <- done:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Catalog) drainPending(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if root, ok := c.takeDirtyRoot(); ok {
			_ = c.reconcileRoot(ctx, root)
			continue
		}
		select {
		case path := <-c.queue:
			c.mu.Lock()
			queued := c.paths[path]
			delete(c.paths, path)
			c.mu.Unlock()
			_ = c.indexQueuedSession(ctx, queued, path)
		case path := <-c.rootCh:
			c.mu.Lock()
			root, ok := c.roots[path]
			c.mu.Unlock()
			if ok {
				_ = c.reconcileRoot(ctx, root)
			}
		default:
			c.mu.Lock()
			empty := len(c.paths) == 0 && len(c.dirtyRoots) == 0
			c.mu.Unlock()
			if empty && len(c.queue) == 0 && len(c.rootCh) == 0 {
				return
			}
			// Another goroutine may have enqueued between checks; yield once.
			runtime.Gosched()
			c.mu.Lock()
			empty = len(c.paths) == 0 && len(c.dirtyRoots) == 0
			c.mu.Unlock()
			if empty && len(c.queue) == 0 && len(c.rootCh) == 0 {
				return
			}
		}
	}
}

func (c *Catalog) markRootDirty(path string) {
	c.mu.Lock()
	c.dirtyRoots[path] = true
	c.mu.Unlock()
}

func (c *Catalog) markAllRootsDirty() {
	c.mu.Lock()
	for path := range c.roots {
		c.dirtyRoots[path] = true
	}
	c.mu.Unlock()
}

func (c *Catalog) takeDirtyRoot() (Root, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for path := range c.dirtyRoots {
		delete(c.dirtyRoots, path)
		root, ok := c.roots[path]
		return root, ok
	}
	return Root{}, false
}

func (c *Catalog) reconcileRoot(ctx context.Context, root Root) error {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	if err := root.validate(); err != nil {
		return err
	}
	paths, err := currentPaths(ctx, &root)
	if err != nil {
		c.setError(err)
		return err
	}
	return c.reconcilePaths(ctx, root, paths)
}

func (c *Catalog) indexQueuedSession(ctx context.Context, root Root, path string) error {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	return c.indexCurrentSession(ctx, root, path, 0)
}

func (c *Catalog) reconcilePaths(ctx context.Context, root Root, paths []string) error {
	now := c.opts.Now().UnixMilli()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO history_roots(path,source,scope,workspace_root,scan_generation,state,total)
        VALUES(?,?,?,?,1,'scanning',?) ON CONFLICT(path) DO UPDATE SET source=excluded.source,scope=excluded.scope,
        workspace_root=excluded.workspace_root,scan_generation=history_roots.scan_generation+1,state='scanning',error='',total=excluded.total
        RETURNING scan_generation`, root.Path, root.Source, root.Scope, root.WorkspaceRoot, len(paths)).Scan(&generation); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	indexed := 0
	for i, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.indexCurrentSession(ctx, root, path, generation); err == nil {
			indexed++
		}
		if (i+1)%32 == 0 {
			runtime.Gosched()
		}
	}
	tx, err = c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE history_sources SET missing_since=CASE WHEN missing_since=0 THEN ? ELSE missing_since END,
        health='missing' WHERE root=? AND seen_generation<>?`, now, root.Path, generation); err != nil {
		_ = tx.Rollback()
		return err
	}
	cutoff := now - c.opts.MissingGrace.Milliseconds()
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_fts WHERE rowid IN (
        SELECT d.id FROM history_documents d JOIN history_sources s ON s.path=d.source_path
        WHERE s.root=? AND s.seen_generation<>? AND s.missing_since>0 AND s.missing_since<=?
    )`, root.Path, generation, cutoff); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_documents WHERE source_path IN (
        SELECT path FROM history_sources WHERE root=? AND seen_generation<>? AND missing_since>0 AND missing_since<=?
    )`, root.Path, generation, cutoff); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_sources
        WHERE root=? AND seen_generation<>? AND missing_since>0 AND missing_since<=?`, root.Path, generation, cutoff); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE history_roots SET state='ready',indexed=?,completed_at=?,scan_cursor='' WHERE path=?`, indexed, now, root.Path); err != nil {
		_ = tx.Rollback()
		return err
	}
	revision, err := bump(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.publish(revision, []string{root.Path}, "reconcile")
	return nil
}

type sourceMetadata struct {
	title, topicID, topicTitle, preview string
	createdAt, lastActivity             int64
}

func (c *Catalog) replaceSource(ctx context.Context, root Root, path string, generation, revision int64,
	digest, metaFingerprint string, messages []provider.Message, meta sourceMetadata) error {
	// Hide stale terms as soon as the authoritative fingerprint changes. Rows
	// remain available for retry and are atomically replaced below.
	if _, err := c.db.ExecContext(ctx, `UPDATE history_sources SET health='stale',last_error='' WHERE path=?`, path); err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_fts WHERE rowid IN (SELECT id FROM history_documents WHERE source_path=?)`, path); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_documents WHERE source_path=?`, path); err != nil {
		_ = tx.Rollback()
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO history_sources(path,root,source,scope,workspace_root,content_revision,content_digest,
        meta_fingerprint,message_count,custom_title,topic_id,topic_title,preview,created_at,
        last_activity_at,health,missing_since,seen_generation,last_error) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'ok',0,?,'')
        ON CONFLICT(path) DO UPDATE SET root=excluded.root,source=excluded.source,scope=excluded.scope,workspace_root=excluded.workspace_root,
        content_revision=excluded.content_revision,content_digest=excluded.content_digest,
        meta_fingerprint=excluded.meta_fingerprint,message_count=excluded.message_count,
        custom_title=excluded.custom_title,topic_id=excluded.topic_id,topic_title=excluded.topic_title,preview=excluded.preview,
        created_at=excluded.created_at,last_activity_at=excluded.last_activity_at,health='ok',missing_since=0,
		seen_generation=CASE WHEN excluded.seen_generation>0 THEN excluded.seen_generation ELSE history_sources.seen_generation END,last_error=''`, path, root.Path, root.Source, root.Scope, root.WorkspaceRoot, revision, digest,
		metaFingerprint, len(messages), meta.title, meta.topicID, meta.topicTitle, meta.preview,
		meta.createdAt, meta.lastActivity, generation)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, doc := range documents(messages) {
		result, err := tx.ExecContext(ctx, `INSERT INTO history_documents(source_path,message_index,part_index,role,kind,tool_name,token_count)
            VALUES(?,?,?,?,?,?,?)`, path, doc.message, doc.part, doc.role, doc.kind, doc.tool, doc.count)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		rowID, err := result.LastInsertId()
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO history_fts(rowid,terms) VALUES(?,?)`, rowID, doc.terms); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	newRevision, err := bump(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.publish(newRevision, []string{root.Path}, "source-indexed")
	return nil
}

func bump(ctx context.Context, tx *sql.Tx) (uint64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE history_state SET revision=revision+1 WHERE id=1`); err != nil {
		return 0, err
	}
	var revision uint64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM history_state WHERE id=1`).Scan(&revision)
	return revision, err
}

func (c *Catalog) Purge(ctx context.Context, path string) error {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	return c.purge(ctx, path)
}

func (c *Catalog) purge(ctx context.Context, path string) error {
	if c == nil {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_fts WHERE rowid IN (SELECT id FROM history_documents WHERE source_path=?)`, path); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_sources WHERE path=?`, path); err != nil {
		_ = tx.Rollback()
		return err
	}
	revision, err := bump(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, _ = c.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	_, _ = c.db.ExecContext(ctx, `PRAGMA incremental_vacuum(64)`)
	c.publish(revision, nil, "purge")
	return nil
}

func (c *Catalog) refreshStatus(ctx context.Context) {
	var indexed, total, pending, failed int64
	_ = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM history_sources WHERE health='ok'`).Scan(&indexed)
	_ = c.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(total),0) FROM history_roots`).Scan(&total)
	_ = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM history_roots WHERE state<>'ready'`).Scan(&pending)
	_ = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM history_sources WHERE health='corrupt'`).Scan(&failed)
	c.statusMu.Lock()
	c.status.Indexed, c.status.Total, c.status.Pending, c.status.Failed = indexed, total, pending, failed
	c.status.Revision = c.revision.Load()
	c.statusMu.Unlock()
}
func (c *Catalog) publish(revision uint64, roots []string, reason string) {
	c.revision.Store(revision)
	statusCtx := c.ctx
	if statusCtx == nil {
		statusCtx = context.Background()
	}
	c.refreshStatus(statusCtx)
	if c.opts.OnRevision != nil {
		c.opts.OnRevision(c.Status(), roots, reason)
	}
}

func (c *Catalog) setError(err error) {
	c.statusMu.Lock()
	c.status.LastError = err.Error()
	c.status.Failed++
	c.statusMu.Unlock()
}

func (c *Catalog) Status() Status {
	if c == nil {
		return Status{State: "degraded", Mode: projectiondb.ModeMemory, LastError: "history catalog unavailable"}
	}
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status
}

func (c *Catalog) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.cancel()
		go func() {
			c.wg.Wait()
			c.closeErr = c.db.Close()
			close(c.closeDone)
		}()
	})
	select {
	case <-c.closeDone:
		return c.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
