package history

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"deepzora/internal/agent"
	"deepzora/internal/historycatalog"
	"deepzora/internal/provider"
	"deepzora/internal/retrieval"
	"deepzora/internal/session"
)

type IndexedSearcher struct {
	sources []Source
	partial atomic.Bool
	initErr error
}

func catalogID(query *session.Query) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(query.StorageRoot())))
}

func NewIndexedSearcher(opts Options) *IndexedSearcher {
	s := &IndexedSearcher{sources: append([]Source(nil), opts.Sources...)}
	for _, parent := range opts.Sources {
		if parent.Query == nil {
			continue
		}
		child, err := childHistorySource(parent)
		if err != nil {
			s.initErr = err
			break
		}
		s.sources = append(s.sources, child)
	}
	roots := []historycatalog.Root{}
	for _, source := range s.sources {
		if source.Query != nil {
			roots = append(roots, historycatalog.CurrentRoot(source.Query))
		}
	}
	processHistoryCatalog.register(roots)
	return s
}

type currentEntry struct {
	source Source
	info   session.SessionInfo
}

func (s *IndexedSearcher) entries(ctx context.Context, scope string) (map[string]currentEntry, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}
	entries := map[string]currentEntry{}
	for _, source := range s.sources {
		if scope == scopeProject && source.Scope != scopeProject {
			continue
		}
		if source.Query == nil {
			return nil, fmt.Errorf("history: current session catalog is unavailable")
		}
		var allowed map[string]bool
		if source.Membership != nil {
			var err error
			allowed, err = source.Membership(ctx)
			if err != nil {
				return nil, err
			}
		}
		for cursor := ""; ; {
			page, err := source.Query.List(ctx, cursor, 100)
			if err != nil {
				return nil, err
			}
			for _, info := range page.Sessions {
				if info.Error != "" || info.Path == "" || (source.Membership != nil && !allowed[info.SessionID]) {
					continue
				}
				if _, found := entries[info.Path]; !found {
					entries[info.Path] = currentEntry{source, info}
				}
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor <= cursor {
				return nil, fmt.Errorf("history: catalog cursor did not advance")
			}
			cursor = page.NextCursor
		}
	}
	return entries, nil
}

func (e currentEntry) read(ctx context.Context) ([]provider.Message, string, error) {
	if e.source.Membership != nil {
		allowed, err := e.source.Membership(ctx)
		if err != nil {
			return nil, "", err
		}
		if !allowed[e.info.SessionID] {
			return nil, "", fmt.Errorf("history: session is outside current membership")
		}
	}
	messages, digest, info, err := historycatalog.ReadCurrent(ctx, e.source.Query, e.info.Ref)
	if err == nil && info.Path != e.info.Path {
		return nil, "", fmt.Errorf("history: catalog identity changed")
	}
	return messages, digest, err
}

func (s *IndexedSearcher) Search(ctx context.Context, req SearchRequest) ([]Hit, error) {
	query := strings.TrimSpace(req.Query)
	terms, err := retrieval.QueryTerms(query)
	if err != nil {
		return nil, err
	}
	scope, err := normalizeScope(req.Scope)
	if err != nil {
		return nil, err
	}
	kinds, err := normalizeKinds(req.Kinds)
	if err != nil {
		return nil, err
	}
	entries, err := s.entries(ctx, scope)
	if err != nil {
		return nil, err
	}
	catalog := processHistoryCatalog.get()
	if catalog == nil {
		return []Hit{}, nil
	}
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	kindNames := make([]string, 0, len(kinds))
	for kind := range kinds {
		kindNames = append(kindNames, string(kind))
	}
	sort.Strings(kindNames)
	limit := clamp(req.Limit, defaultLimit, maxLimit)
	result, err := catalog.Search(ctx, historycatalog.SearchRequest{Query: query, Paths: paths,
		Kinds: kindNames, ToolName: strings.TrimSpace(req.ToolName), Limit: min(limit*4, historycatalog.MaxLimit)})
	if err != nil {
		return nil, err
	}
	type body struct {
		messages []provider.Message
		digest   string
		err      error
	}
	loaded := map[string]body{}
	hits := []Hit{}
	infos := make([]session.SessionInfo, 0, len(entries))
	for _, entry := range entries {
		infos = append(infos, entry.info)
	}
	covered, err := catalog.CoversCurrent(ctx, infos)
	if err != nil {
		return nil, err
	}
	partial := !covered
	for _, candidate := range result.Items {
		entry, allowed := entries[candidate.SessionPath]
		if !allowed {
			continue
		}
		content, ok := loaded[candidate.SessionPath]
		if !ok {
			content.messages, content.digest, content.err = entry.read(ctx)
			loaded[candidate.SessionPath] = content
		}
		if content.err != nil || content.digest != candidate.ContentDigest {
			partial = true
			catalog.EnqueueExisting(ctx, candidate.SessionPath)
			continue
		}
		text, ok := candidateText(content.messages, candidate)
		if !ok {
			partial = true
			catalog.EnqueueExisting(ctx, candidate.SessionPath)
			continue
		}
		hits = append(hits, Hit{Score: candidate.Score, CatalogID: catalogID(entry.source.Query), SessionID: entry.info.SessionID, ContentDigest: content.digest,
			Source: entry.source.Scope, MessageIndex: candidate.MessageIndex, Role: provider.Role(candidate.Role),
			Kind: Kind(candidate.Kind), ToolName: candidate.ToolName, Snippet: retrieval.MakeSnippet(text, query, terms, maxSnippet)})
	}
	s.partial.Store(partial)
	// Refresh in the background: a new session/commit need not wait for a
	// controller rebuild or the periodic sweep to enter the index.
	for _, source := range s.sources {
		if source.Query != nil {
			catalog.RegisterRoot(historycatalog.CurrentRoot(source.Query))
		}
	}
	hits = retrieval.KeepTopRelativeScore(hits, scoreFloor, func(hit Hit) float64 { return hit.Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (s *IndexedSearcher) Around(ctx context.Context, req AroundRequest) ([]MessageContext, error) {
	if strings.TrimSpace(req.CatalogID) == "" || strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.ContentDigest) == "" {
		return nil, fmt.Errorf("catalog_id, session_id and content_digest from a search hit are required")
	}
	if req.MessageIndex < 0 {
		return nil, fmt.Errorf("message_index must be non-negative")
	}
	entries, err := s.entries(ctx, scopeGlobal)
	if err != nil {
		return nil, err
	}
	var selected *currentEntry
	for _, entry := range entries {
		if entry.info.SessionID != req.SessionID || catalogID(entry.source.Query) != req.CatalogID {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("history: ambiguous session identity")
		}
		selected = &entry
	}
	if selected == nil {
		return nil, fmt.Errorf("history: session is outside current membership")
	}
	messages, digest, err := selected.read(ctx)
	if err != nil {
		return nil, err
	}
	if digest != req.ContentDigest {
		return nil, fmt.Errorf("history: stale search hit; search again")
	}
	if req.MessageIndex >= len(messages) {
		return nil, fmt.Errorf("message_index is outside session length")
	}
	start := max(0, req.MessageIndex-clamp(req.Before, defaultAround, maxAround))
	end := req.MessageIndex + 1 + min(clamp(req.After, defaultAround, maxAround), len(messages)-req.MessageIndex-1)
	out := []MessageContext{}
	for i := start; i < end; i++ {
		if !agent.IsPinnedContextRevision(messages[i]) {
			out = append(out, MessageContext{Index: i, Text: renderMessage(i, messages[i])})
		}
	}
	return out, nil
}

func (s *IndexedSearcher) IndexStatus() historycatalog.Status {
	if catalog := processHistoryCatalog.get(); catalog != nil {
		status := catalog.Status()
		if s.partial.Load() {
			status.State = "building"
		}
		return status
	}
	return historycatalog.Status{State: "opening", Pending: 1}
}
