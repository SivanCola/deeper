package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"deepzora/internal/history"
	"deepzora/internal/historycatalog"
	"deepzora/internal/retrieval"
	"deepzora/internal/session"
)

type historySearchSnapshotBuild struct {
	ctx      context.Context
	app      *App
	store    *readSnapshotStore
	snapshot *readSnapshot
	catalog  *historycatalog.Catalog
	request  HistorySearchRequest
	active   string
	meta     HistorySearchPage
	fence    *historyCurrentFence
	entries  map[string]historyCurrentSource
	overlays map[string]catalogRuntimeOverlay
	terms    []string
	cutoff   time.Time
	needed   map[string]map[historyTextKey]struct{}
	order    []string
	sources  map[string]*historySearchSource
}

// historySearchSource is one session file read once per build: the digest it
// had and the snippet of every candidate that points into it.
type historySearchSource struct {
	digest   string
	intact   bool
	snippets map[historyTextKey]string
}

type historyTextKey struct {
	message, part int
	kind          string
}

var loadHistorySearchMessages = historycatalog.ReadCurrent

func (a *App) buildHistorySearchSnapshot(ctx context.Context, store *readSnapshotStore, snap *readSnapshot, req HistorySearchRequest, active string) error {
	status := a.GetHistoryIndexStatus()
	meta := HistorySearchPage{Status: status, Revision: status.Revision, Partial: status.State != "ready" || status.Pending > 0}
	catalog := history.SharedCatalog()
	if catalog == nil || req.Query == "" {
		snap.metadata, _ = json.Marshal(meta)
		return nil
	}
	candidates := &readSnapshot{}
	defer store.dispose(candidates)
	entries, err := a.currentHistorySources(ctx, req, func(entry historyCurrentSource) error {
		return store.reserve(snap, int64(1024+len(entry.info.Path)*2+len(entry.title)+len(entry.topicTitle)))
	})
	if err != nil {
		return err
	}
	paths, infos := []string{}, []session.SessionInfo{}
	for path, entry := range entries {
		paths, infos = append(paths, path), append(infos, entry.info)
	}
	covered, err := catalog.CoversCurrent(ctx, infos)
	if err != nil {
		return err
	}
	meta.Partial = meta.Partial || !covered
	// Registry membership is applied before SQL ranking. Index scope metadata
	// is disposable and cannot authorize reads or choose a project owner.
	search := historycatalog.SearchRequest{Query: req.Query, Paths: paths, Kinds: req.Kinds, ToolName: req.ToolName}
	if err := catalog.CaptureSearch(ctx, search, func(row historycatalog.Candidate) error { return store.append(ctx, candidates, row) }); err != nil {
		return err
	}
	terms, err := retrieval.QueryTerms(req.Query)
	if err != nil {
		return err
	}
	fence := &historyCurrentFence{app: a, entries: map[string]historyCurrentSource{}, generations: map[string]string{}}
	overlays := a.catalogSessionRuntimeOverlays()
	build := &historySearchSnapshotBuild{ctx: ctx, app: a, store: store, snapshot: snap, catalog: catalog, request: req, active: active, meta: meta, fence: fence, entries: entries, overlays: overlays, terms: terms, cutoff: time.Now()}
	if err := build.run(candidates); err != nil {
		return err
	}
	snap.validate = fence.validate
	snap.metadata, err = json.Marshal(build.meta)
	return err
}

// run keeps the bm25 order the catalog returned while reading each session file
// once: hits interleave sessions, so loading per hit rereads a file per row.
func (b *historySearchSnapshotBuild) run(candidates *readSnapshot) error {
	b.needed, b.sources = map[string]map[historyTextKey]struct{}{}, map[string]*historySearchSource{}
	if err := candidates.walk(b.ctx, b.collect); err != nil {
		return err
	}
	for _, path := range b.order {
		if err := b.loadSource(path, b.needed[path]); err != nil {
			return err
		}
		delete(b.needed, path)
	}
	return candidates.walk(b.ctx, b.visit)
}

func (b *historySearchSnapshotBuild) candidate(encoded []byte) (historycatalog.Candidate, catalogRuntimeOverlay, bool, error) {
	if err := b.ctx.Err(); err != nil {
		return historycatalog.Candidate{}, catalogRuntimeOverlay{}, false, err
	}
	var row historycatalog.Candidate
	if err := json.Unmarshal(encoded, &row); err != nil {
		return row, catalogRuntimeOverlay{}, false, err
	}
	entry, allowed := b.entries[row.SessionPath]
	if !allowed {
		return row, catalogRuntimeOverlay{}, false, nil
	}
	overlay := catalogRuntimeOverlay{}
	current := false
	if entry.catalog == historySessionCatalog {
		route := sessionRoute(entry.info.SessionID)
		overlay, current = b.overlays[sessionRuntimeKey(route)], route == b.active
	}
	keep := historyStatusMatches(b.request.Status, overlay.open, current) && historyTimeMatchesAt(unixMillis(entry.info.UpdatedAt), b.request.TimeFilter, b.cutoff)
	return row, overlay, keep, nil
}

func (b *historySearchSnapshotBuild) collect(encoded []byte) error {
	row, _, keep, err := b.candidate(encoded)
	if err != nil || !keep {
		return err
	}
	keys, ok := b.needed[row.SessionPath]
	if !ok {
		if err := b.store.reserve(b.snapshot, int64(256+len(row.SessionPath)*2)); err != nil {
			return err
		}
		keys = map[historyTextKey]struct{}{}
		b.needed[row.SessionPath] = keys
		b.order = append(b.order, row.SessionPath)
	}
	key := historyTextKey{message: row.MessageIndex, part: row.PartIndex, kind: row.Kind}
	if _, exists := keys[key]; !exists {
		if err := b.store.reserve(b.snapshot, int64(128+len(row.Kind))); err != nil {
			return err
		}
		keys[key] = struct{}{}
	}
	return nil
}

func (b *historySearchSnapshotBuild) visit(encoded []byte) error {
	row, overlay, keep, err := b.candidate(encoded)
	if err != nil || !keep {
		return err
	}
	src := b.sources[row.SessionPath]
	if !src.intact || row.ContentDigest == "" || src.digest != row.ContentDigest {
		b.catalog.EnqueueExisting(b.ctx, row.SessionPath)
		b.meta.Partial = true
		return nil
	}
	snippet, ok := src.snippets[historyTextKey{message: row.MessageIndex, part: row.PartIndex, kind: row.Kind}]
	if !ok {
		b.catalog.EnqueueExisting(b.ctx, row.SessionPath)
		b.meta.Partial = true
		return nil
	}
	entry := b.entries[row.SessionPath]
	hit := HistorySearchHit{Catalog: entry.catalog, SessionID: entry.info.SessionID, Source: entry.scope, MessageIndex: row.MessageIndex, PartIndex: row.PartIndex, ContentDigest: src.digest, Role: row.Role, Kind: row.Kind, ToolName: row.ToolName, Snippet: snippet, Score: row.Score, SessionTitle: entry.title, TopicTitle: entry.topicTitle, WorkspaceRoot: entry.workspaceRoot, LastActivityAt: unixMillis(entry.info.UpdatedAt), Open: overlay.open, Running: overlay.running, Current: entry.catalog == historySessionCatalog && sessionRoute(entry.info.SessionID) == b.active}
	return b.store.append(b.ctx, b.snapshot, hit)
}

func (b *historySearchSnapshotBuild) loadSource(path string, keys map[historyTextKey]struct{}) error {
	ctx := b.ctx
	src := &historySearchSource{}
	b.sources[path] = src
	entry := b.entries[path]
	generation, err := entry.query.StorageGeneration(ctx, entry.info.Ref)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, session.ErrSessionNotFound) {
			return err
		}
		b.meta.Partial = true
		return nil
	}
	messages, digest, info, err := loadHistorySearchMessages(ctx, entry.query, entry.info.Ref)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, session.ErrSessionNotFound) {
		b.meta.Partial = true
		return nil
	}
	if err != nil {
		return err
	}
	if info.Path != path {
		return snapshotStale("lifecycle_changed")
	}
	b.fence.entries[path], b.fence.generations[path] = entry, generation
	src.digest, src.intact = digest, true
	src.snippets = make(map[historyTextKey]string, len(keys))
	for key := range keys {
		text, ok := desktopHistoryText(messages, historycatalog.Candidate{MessageIndex: key.message, PartIndex: key.part, Kind: key.kind})
		if ok {
			snippet := retrieval.MakeSnippet(text, b.request.Query, b.terms, 240)
			if err := b.store.reserve(b.snapshot, int64(len(snippet)+64)); err != nil {
				return err
			}
			src.snippets[key] = snippet
		}
	}
	return nil
}
