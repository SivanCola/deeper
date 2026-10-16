package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"deepzora/internal/projectiondb"
	"deepzora/internal/sessioncontent"
)

const (
	historyIndexVersion     = 1
	HistoryPageDefaultLimit = 100
	HistoryPageMaxLimit     = 500
	HistoryPageMaxBytes     = 2 << 20
	historyIndexTxnEvents   = 512
	historyIndexTxnBytes    = 8 << 20
)

// PersistentMessage is the storage/query representation of a message. It is
// deliberately separate from provider.Message: provider DTOs are materialized
// only at model or compatibility boundaries.
type PersistentMessage struct {
	ToolObservations map[string]ToolObservation `json:"toolObservations,omitempty"`
	SubmissionID     string                     `json:"submissionId,omitempty"`
	SamplingCount    *int                       `json:"samplingCount,omitempty"`
	ToolCount        *int                       `json:"toolCount,omitempty"`
	TurnFinal        bool                       `json:"turnFinal,omitempty"`
	TurnDurationMs   int64                      `json:"turnDurationMs,omitempty"`
	MessageID        string                     `json:"messageId"`
	Position         int64                      `json:"position"`
	Version          int                        `json:"version"`
	Role             string                     `json:"role"`
	Preview          string                     `json:"preview,omitempty"`
	EventSequence    uint64                     `json:"eventSequence"`
	VisibleTurn      int                        `json:"visibleTurn"`
	Inline           json.RawMessage            `json:"inline,omitempty"`
	ContentRef       *sessioncontent.Ref        `json:"contentRef,omitempty"`
}

type MessageHistoryPage struct {
	Messages         []PersistentMessage `json:"messages"`
	SnapshotSequence uint64              `json:"snapshotSequence"`
	CoverageSequence uint64              `json:"coverageSequence"`
	Status           string              `json:"status"`
	TotalTurns       int                 `json:"totalTurns"`
	Generation       string              `json:"generation"`
	NextCursor       string              `json:"nextCursor,omitempty"`
	HasMore          bool                `json:"hasMore"`
}

// MessageLocation converts a stable message identity into a cursor for a
// fixed locator snapshot. It contains no message body.
type MessageLocation struct {
	Status           string `json:"status"`
	MessageID        string `json:"messageId,omitempty"`
	SnapshotSequence uint64 `json:"snapshotSequence"`
	CoverageSequence uint64 `json:"coverageSequence"`
	Generation       string `json:"generation,omitempty"`
	Position         int64  `json:"position,omitempty"`
	VisibleTurn      int    `json:"visibleTurn,omitempty"`
	Cursor           string `json:"cursor,omitempty"`
}

type historyCursor struct {
	SessionID        string `json:"sessionId"`
	StorageRevision  int    `json:"storageRevision"`
	SnapshotSequence uint64 `json:"snapshotSequence"`
	BeforePosition   int64  `json:"beforePosition"`
	Projection       int    `json:"projection"`
	Generation       string `json:"generation"`
}

type historyBuildState struct {
	commitTurn   string
	commitTime   int64
	nextPosition int64
	visibleTurn  int
	positions    map[string]int64
	turns        map[string]int
	versions     map[string]int
	tx           *sql.Tx
	statements   *historyBuildStatements
	transactions [][]any
	events       [][]any
	contentRefs  [][]any
	messages     [][]any
}

type historyPreparation struct {
	done  chan struct{}
	err   error
	owner context.Context
}

type historyBuildStatements struct {
	clear  *sql.Stmt
	expire *sql.Stmt
}

func prepareHistoryBuildStatements(ctx context.Context, tx *sql.Tx) (*historyBuildStatements, error) {
	statements := &historyBuildStatements{}
	queries := []struct {
		target **sql.Stmt
		query  string
	}{
		{&statements.clear, `UPDATE messages SET current=0,valid_to=? WHERE current=1`},
		{&statements.expire, `UPDATE messages SET current=0,valid_to=? WHERE message_id=? AND current=1`},
	}
	for _, candidate := range queries {
		prepared, err := tx.PrepareContext(ctx, candidate.query)
		if err != nil {
			statements.close()
			return nil, err
		}
		*candidate.target = prepared
	}
	return statements, nil
}

func (s *historyBuildStatements) close() {
	if s == nil {
		return
	}
	for _, statement := range []*sql.Stmt{s.clear, s.expire} {
		if statement != nil {
			_ = statement.Close()
		}
	}
}

func historyIndexPath(root, sessionID string) string {
	return filepath.Join(root, ".query-cache", filepath.Base(sessionID), "history-locator.sqlite")
}

type SearchHistoryHit struct {
	MessageID     string `json:"messageId"`
	Position      int64  `json:"position"`
	Role          string `json:"role"`
	Preview       string `json:"preview"`
	EventSequence uint64 `json:"eventSequence"`
}

type SearchHistoryPage struct {
	Hits             []SearchHistoryHit `json:"hits"`
	SnapshotSequence uint64             `json:"snapshotSequence"`
	CoverageSequence uint64             `json:"coverageSequence"`
	Status           string             `json:"status"`
	NextCursor       string             `json:"nextCursor,omitempty"`
	HasMore          bool               `json:"hasMore"`
}

type searchHistoryCursor struct {
	SessionID        string `json:"sessionId"`
	StorageRevision  int    `json:"storageRevision"`
	SnapshotSequence uint64 `json:"snapshotSequence"`
	BeforePosition   int64  `json:"beforePosition"`
	Projection       int    `json:"projection"`
	QueryDigest      string `json:"queryDigest"`
	Generation       string `json:"generation"`
}

func (q *Query) HistoryPage(ctx context.Context, ref SessionRef, cursor string, limit int) (MessageHistoryPage, error) {
	if q == nil {
		return MessageHistoryPage{}, errors.New("session: nil query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return MessageHistoryPage{}, err
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return MessageHistoryPage{}, errors.New("session: history index requires filesystem persistence")
	}
	if limit <= 0 {
		limit = HistoryPageDefaultLimit
	}
	limit = min(limit, HistoryPageMaxLimit)
	path := historyIndexPath(filesystem.Root, ref.SessionID)
	ready, err := q.historyLocatorReady(ctx, filesystem, ref.SessionID, path)
	if err != nil {
		return MessageHistoryPage{}, err
	}
	if !ready {
		return MessageHistoryPage{Messages: []PersistentMessage{}, Status: "preparing"}, nil
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historySchema, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return MessageHistoryPage{}, err
	}
	defer handle.DB.Close()
	metadata, err := readHistoryIndexMetadata(ctx, handle.DB)
	if err != nil {
		return MessageHistoryPage{}, err
	}
	snapshot := metadata.durableSequence
	// Empty cursor means the newest page. Subsequent cursors move toward older
	// positions while the snapshot sequence remains fixed.
	before := int64(^uint64(0) >> 1)
	if cursor != "" {
		parsed, err := decodeHistoryCursor(cursor)
		if err != nil {
			return MessageHistoryPage{}, err
		}
		if parsed.SessionID != ref.SessionID || parsed.StorageRevision != StorageRevision || parsed.Projection != historyIndexVersion || parsed.SnapshotSequence > snapshot || parsed.Generation != metadata.generation {
			return MessageHistoryPage{Messages: []PersistentMessage{}, Status: "stale_cursor", CoverageSequence: metadata.durableSequence, Generation: metadata.generation}, nil
		}
		snapshot = parsed.SnapshotSequence
		if parsed.BeforePosition <= 0 {
			return MessageHistoryPage{}, errors.New("session: invalid history cursor position")
		}
		before = parsed.BeforePosition
	}
	return q.readMessageHistoryPage(ctx, handle.DB, filesystem, ref, metadata, snapshot, before, limit)
}

func (q *Query) readMessageHistoryPage(ctx context.Context, db *sql.DB, filesystem *FilesystemPersistence, ref SessionRef, metadata historyIndexMetadata, snapshot uint64, before int64, limit int) (MessageHistoryPage, error) {
	page := MessageHistoryPage{Messages: []PersistentMessage{}, SnapshotSequence: snapshot, CoverageSequence: metadata.durableSequence, Status: "ready", Generation: metadata.generation}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(visible_turn),0) FROM messages WHERE event_sequence<=? AND (valid_to=0 OR valid_to>?)`, snapshot, snapshot).Scan(&page.TotalTurns); err != nil {
		return MessageHistoryPage{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT message_id,position,version,role,preview,event_sequence,visible_turn,inline,content_digest,content_bytes,content_index_digest,COALESCE((SELECT submission_id FROM submissions WHERE submissions.message_id=messages.message_id AND submissions.sequence<=messages.event_sequence AND submissions.session_id=(SELECT value FROM metadata WHERE key='session_id') LIMIT 1),'') FROM messages WHERE position<? AND event_sequence<=? AND (valid_to=0 OR valid_to>?) ORDER BY position DESC LIMIT ?`, before, snapshot, snapshot, limit+1)
	if err != nil {
		return MessageHistoryPage{}, err
	}
	defer rows.Close()
	encodedBytes := 0
	storageGeneration := q.storageGeneration(ref.SessionID)
	for rows.Next() {
		var message PersistentMessage
		var inline []byte
		var digest, indexDigest string
		var contentBytes int64
		if err := rows.Scan(&message.MessageID, &message.Position, &message.Version, &message.Role, &message.Preview, &message.EventSequence, &message.VisibleTurn, &inline, &digest, &contentBytes, &indexDigest, &message.SubmissionID); err != nil {
			return MessageHistoryPage{}, err
		}
		if len(page.Messages) == limit {
			page.HasMore = true
			break
		}
		message.Inline = append(json.RawMessage(nil), inline...)
		if digest != "" {
			contentRef := sessioncontent.Ref{Digest: digest, Bytes: contentBytes, IndexDigest: indexDigest, IntegrityBlock: sessioncontent.IntegrityBlockBytes, MediaType: "application/json"}
			if contentBytes <= recentInlineBytes && encodedBytes+int(contentBytes) <= HistoryPageMaxBytes {
				body, readErr := contentStoreForSessionDir(filepath.Join(filesystem.Root, ref.SessionID)).ReadRange(ctx, contentRef, 0, contentBytes)
				if readErr != nil {
					return MessageHistoryPage{}, readErr
				}
				message.Inline = json.RawMessage(body)
			} else {
				message.ContentRef = &contentRef
				q.authorizeContentForGeneration(ref.SessionID, storageGeneration, digest, contentBytes, indexDigest)
			}
		}
		encoded, _ := json.Marshal(message)
		if len(page.Messages) > 0 && encodedBytes+len(encoded) > HistoryPageMaxBytes {
			page.HasMore = true
			break
		}
		encodedBytes += len(encoded)
		page.Messages = append(page.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return MessageHistoryPage{}, err
	}
	if page.HasMore && len(page.Messages) > 0 {
		oldest := page.Messages[len(page.Messages)-1]
		page.NextCursor, err = encodeHistoryCursor(historyCursor{SessionID: ref.SessionID, StorageRevision: StorageRevision, SnapshotSequence: snapshot, BeforePosition: oldest.Position, Projection: historyIndexVersion, Generation: metadata.generation})
		if err != nil {
			return MessageHistoryPage{}, err
		}
	}
	slices.Reverse(page.Messages)
	return page, nil
}

// LocateMessage resolves a search hit or durable message id without scanning
// message bodies. A zero snapshot selects the locator's latest covered cut.
func (q *Query) LocateMessage(ctx context.Context, ref SessionRef, messageID string, snapshot uint64) (MessageLocation, error) {
	if q == nil {
		return MessageLocation{}, errors.New("session: nil query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return MessageLocation{}, err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return MessageLocation{}, errors.New("session: message id is required")
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return MessageLocation{}, errors.New("session: history locator requires filesystem persistence")
	}
	path := historyIndexPath(filesystem.Root, ref.SessionID)
	ready, err := q.historyLocatorReady(ctx, filesystem, ref.SessionID, path)
	if err != nil {
		return MessageLocation{}, err
	}
	if !ready {
		return MessageLocation{Status: "preparing", MessageID: messageID}, nil
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historySchema, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return MessageLocation{}, err
	}
	defer handle.DB.Close()
	metadata, err := readHistoryIndexMetadata(ctx, handle.DB)
	if err != nil {
		return MessageLocation{}, err
	}
	if snapshot == 0 {
		snapshot = metadata.durableSequence
	}
	location := MessageLocation{Status: "ready", MessageID: messageID, SnapshotSequence: snapshot, CoverageSequence: metadata.durableSequence, Generation: metadata.generation}
	if snapshot > metadata.durableSequence {
		location.Status = "preparing"
		return location, nil
	}
	err = handle.DB.QueryRowContext(ctx, `SELECT position,visible_turn FROM messages WHERE message_id=? AND event_sequence<=? AND (valid_to=0 OR valid_to>?) ORDER BY version DESC LIMIT 1`, messageID, snapshot, snapshot).Scan(&location.Position, &location.VisibleTurn)
	if errors.Is(err, sql.ErrNoRows) {
		location.Status = "not_found"
		return location, nil
	}
	if err != nil {
		return MessageLocation{}, err
	}
	location.Cursor, err = encodeHistoryWindowCursor(historyWindowCursor{SessionID: ref.SessionID, StorageRevision: StorageRevision, SnapshotSequence: snapshot, Boundary: location.Position + 1, Direction: historyWindowDirOlder, Projection: historyIndexVersion, Generation: metadata.generation})
	return location, err
}

func (q *Query) prepareHistoryLocator(filesystem *FilesystemPersistence, sessionID, path string, callers ...context.Context) *historyPreparation {
	ctx := q.historyReadContext(sessionID, callers...)
	q.historyMu.Lock()
	if current := q.historyBuilds[sessionID]; current != nil && current.owner == ctx {
		select {
		case <-current.done:
			if current.err != nil && !errors.Is(current.err, context.Canceled) {
				q.historyMu.Unlock()
				return current
			}
			// A completed build may have been invalidated by a newer append.
		default:
			q.historyMu.Unlock()
			return current
		}
	}
	preparation := &historyPreparation{done: make(chan struct{}), owner: ctx}
	q.historyBuilds[sessionID] = preparation
	q.historyMu.Unlock()
	q.rebuildMu.Lock()
	if q.closed {
		q.rebuildMu.Unlock()
		preparation.err = context.Canceled
		close(preparation.done)
		return preparation
	}
	q.rebuildWG.Add(1)
	q.rebuildMu.Unlock()
	go func() {
		defer q.rebuildWG.Done()
		// A user-requested history page or locate: highest slot priority.
		if err := q.slots.acquire(ctx, rebuildPriorityUser); err != nil {
			preparation.err = err
			close(preparation.done)
			return
		}
		defer q.slots.release()
		release, err := q.acquireHistoryPreparation(ctx)
		if err != nil {
			preparation.err = err
			close(preparation.done)
			return
		}
		defer release()
		lock := q.projectionLock("history", sessionID)
		lock.Lock()
		preparation.err = ensureHistoryIndex(ctx, filesystem, sessionID, path)
		lock.Unlock()
		close(preparation.done)
	}()
	return preparation
}
