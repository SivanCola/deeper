package main

import (
	"context"
	"errors"
	"sync"

	"deepzora/internal/session"
	"deepzora/internal/sessioncontent"
)

type SessionHistoryReadHandle struct {
	ID                string `json:"id"`
	SessionGeneration uint64 `json:"sessionGeneration"`
}

type desktopHistoryReader struct {
	handle                             SessionHistoryReadHandle
	ctx                                context.Context
	release                            func()
	query                              *session.Query
	ref                                session.SessionRef
	tab                                *WorkspaceTab
	tabID, identity, storageGeneration string
}

type desktopHistoryReaders struct {
	mu      sync.Mutex
	entries map[string]*desktopHistoryReader
	closed  bool
}

// BeginSessionHistoryReadForTab captures the current identity once. Reader
// cancellation owns query preparation, never an execution runtime or writer.
func (a *App) BeginSessionHistoryReadForTab(tabID string) (SessionHistoryReadHandle, error) {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		a.mu.RUnlock()
		return SessionHistoryReadHandle{}, errors.New("history tab unavailable")
	}
	id, generation := tab.SessionID, tab.SessionGeneration
	a.mu.RUnlock()
	query, ref, err := a.canonicalSessionQuery(tabID)
	if err != nil {
		return SessionHistoryReadHandle{}, err
	}
	if ref.SessionID != id {
		return SessionHistoryReadHandle{}, errors.New("history source changed")
	}
	storageGeneration, err := query.StorageGeneration(a.bootContext(), ref)
	if err != nil {
		return SessionHistoryReadHandle{}, err
	}
	shared, release, err := query.AcquireHistoryReader(ref, storageGeneration)
	if err != nil {
		return SessionHistoryReadHandle{}, err
	}
	ctx, cancel := context.WithCancel(shared)
	reader := &desktopHistoryReader{tab: tab, tabID: tabID, identity: id,
		storageGeneration: storageGeneration, query: query, ref: ref, ctx: ctx,
		release: func() { cancel(); release() },
		handle:  SessionHistoryReadHandle{ID: newSessionRuntimeID("history"), SessionGeneration: generation}}
	if !a.historyReaderCurrent(reader) {
		reader.release()
		return SessionHistoryReadHandle{}, errors.New("history source changed")
	}
	a.historyReaders.mu.Lock()
	if a.shuttingDown.Load() || a.historyReaders.closed {
		a.historyReaders.mu.Unlock()
		reader.release()
		return SessionHistoryReadHandle{}, context.Canceled
	}
	if a.historyReaders.entries == nil {
		a.historyReaders.entries = map[string]*desktopHistoryReader{}
	}
	var retired []*desktopHistoryReader
	for key, previous := range a.historyReaders.entries {
		if previous.query == query && previous.ref == ref && previous.storageGeneration != storageGeneration {
			delete(a.historyReaders.entries, key)
			retired = append(retired, previous)
		}
	}
	a.historyReaders.entries[reader.handle.ID] = reader
	a.historyReaders.mu.Unlock()
	for _, previous := range retired {
		previous.release()
	}
	return reader.handle, nil
}

func (a *App) historyReaderCurrent(reader *desktopHistoryReader) bool {
	a.mu.RLock()
	tab := a.tabByIDLocked(reader.tabID)
	current := tab == reader.tab && tab != nil && tab.SessionGeneration == reader.handle.SessionGeneration && tab.SessionID == reader.identity
	a.mu.RUnlock()
	if !current || reader.ctx.Err() != nil {
		return false
	}
	generation, err := reader.query.StorageGeneration(reader.ctx, reader.ref)
	return err == nil && generation == reader.storageGeneration
}

func (a *App) historyReader(id string) (*desktopHistoryReader, error) {
	a.historyReaders.mu.Lock()
	reader := a.historyReaders.entries[id]
	a.historyReaders.mu.Unlock()
	if reader == nil {
		return nil, context.Canceled
	}
	if !a.historyReaderCurrent(reader) {
		a.ReleaseSessionHistoryRead(id)
		return nil, context.Canceled
	}
	return reader, nil
}

// Release is idempotent and affects only the exact handle, never a new reader.
func (a *App) ReleaseSessionHistoryRead(id string) {
	a.historyReaders.mu.Lock()
	reader := a.historyReaders.entries[id]
	delete(a.historyReaders.entries, id)
	a.historyReaders.mu.Unlock()
	if reader != nil {
		reader.release()
	}
}

func (a *App) closeHistoryReaders() {
	a.historyReaders.mu.Lock()
	readers := a.historyReaders.entries
	a.historyReaders.entries = nil
	a.historyReaders.closed = true
	a.historyReaders.mu.Unlock()
	for _, reader := range readers {
		reader.release()
	}
}

func (a *App) ReadSessionHistoryWindow(id string, req session.HistoryWindowRequest) (session.HistoryWindowPage, error) {
	stale := session.HistoryWindowPage{Messages: []session.PersistentMessage{}, Status: "stale_cursor"}
	r, err := a.historyReader(id)
	if err != nil {
		return stale, nil
	}
	page, err := r.query.ReadHistoryWindow(r.ctx, r.ref, req)
	if !a.historyReaderCurrent(r) {
		return stale, nil
	}
	return page, err
}

func (a *App) ReadSessionHistoryOutline(id string, req session.HistoryOutlineRequest) (session.HistoryOutlinePage, error) {
	stale := session.HistoryOutlinePage{Entries: []session.HistoryOutlineEntry{}, Status: "stale_cursor"}
	r, err := a.historyReader(id)
	if err != nil {
		return stale, nil
	}
	page, err := r.query.ReadHistoryOutline(r.ctx, r.ref, req)
	if !a.historyReaderCurrent(r) {
		return stale, nil
	}
	return page, err
}

func (a *App) LocateSessionHistoryMessage(id, messageID string, snapshot uint64) (session.MessageLocation, error) {
	r, err := a.historyReader(id)
	if err != nil {
		return session.MessageLocation{Status: "stale_cursor"}, nil
	}
	location, err := r.query.LocateMessage(r.ctx, r.ref, messageID, snapshot)
	if !a.historyReaderCurrent(r) {
		return session.MessageLocation{Status: "stale_cursor"}, nil
	}
	return location, err
}

func (a *App) SearchSessionHistoryRead(id, text, cursor string, limit int) (session.SearchHistoryPage, error) {
	stale := session.SearchHistoryPage{Hits: []session.SearchHistoryHit{}, Status: "stale_cursor"}
	r, err := a.historyReader(id)
	if err != nil {
		return stale, nil
	}
	page, err := r.query.SearchHistory(r.ctx, r.ref, text, cursor, limit)
	if !a.historyReaderCurrent(r) {
		return stale, nil
	}
	return page, err
}

// ReadSessionHistoryContent keeps lazy content on the same cancellable reader
// that granted the reference. A closed or replaced handle cannot change source.
func (a *App) ReadSessionHistoryContent(id string, ref sessioncontent.Ref, offset int64) (SessionHistoryContentChunk, error) {
	r, err := a.historyReader(id)
	if err != nil {
		return SessionHistoryContentChunk{}, session.ErrStaleGeneration
	}
	chunk, err := readSessionHistoryContent(r.ctx, r.query, r.ref, ref, offset)
	if !a.historyReaderCurrent(r) {
		return SessionHistoryContentChunk{}, session.ErrStaleGeneration
	}
	return chunk, err
}
