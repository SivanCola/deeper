package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	fileencoding "deepzora/internal/fileutil/encoding"
	"deepzora/internal/provider"
	"deepzora/internal/store"
)

const (
	sessionEventSchemaVersion = 1
	// sessionEventReplayMaxBytes caps decoder input before encoding/json can
	// allocate an arbitrarily large record. The ceiling still accommodates
	// image-bearing histories while keeping corrupt logs from exhausting RAM.
	sessionEventReplayMaxBytes = int64(128 << 20)
	// A byte limit alone is insufficient: a compact JSON array can expand into
	// a much larger graph of messages and event records after decoding.
	sessionEventReplayMaxRecords         = 100_000
	sessionEventReplayMaxMessages        = 100_000
	sessionEventReplayMaxCollectionItems = 100_000
	sessionEventProbeMaxBytes            = int64(4 << 10)
	// sessionEventLogCompactFloor is the smallest log size that can trigger
	// event-log maintenance, so short sessions never pay a checkpoint rewrite.
	sessionEventLogCompactFloor = int64(256 << 10)
	// sessionEventLogCompactFactor bounds the log at this multiple of the live
	// transcript's encoded size; past it the log is rewritten to one replace
	// event so replace-heavy histories (rewind and recovery) cannot grow the
	// file without bound.
	sessionEventLogCompactFactor = int64(4)
)

// ErrSessionReplayLimitExceeded identifies a session that was left untouched
// because replaying it would exceed the process safety budget. Callers must not
// fall back to an older checkpoint: the event log may contain newer turns.
var ErrSessionReplayLimitExceeded = errors.New("session history exceeds safe replay limits")

// ErrSessionHistoryDamaged marks a frozen source whose authoritative event
// log cannot be proven complete. Migration must fail closed instead of falling
// back to an older checkpoint and silently dropping newer turns.
var ErrSessionHistoryDamaged = errors.New("session history is damaged")

// SessionReplayLimitError carries machine-readable diagnostics while keeping
// Error free of local paths for Desktop surfaces that display startup errors.
type SessionReplayLimitError struct {
	Path     string
	Resource string
	Value    int64
	Limit    int64
}

func (e *SessionReplayLimitError) Error() string {
	if e == nil {
		return ErrSessionReplayLimitExceeded.Error()
	}
	return fmt.Sprintf("%s: %s=%d, limit=%d; session files were left unchanged",
		ErrSessionReplayLimitExceeded, e.Resource, e.Value, e.Limit)
}

func (e *SessionReplayLimitError) Unwrap() error {
	return ErrSessionReplayLimitExceeded
}

type sessionReplayLimits struct {
	maxBytes           int64
	maxRecords         int
	maxMessages        int
	maxCollectionItems int
}

var defaultSessionReplayLimits = sessionReplayLimits{
	maxBytes:           sessionEventReplayMaxBytes,
	maxRecords:         sessionEventReplayMaxRecords,
	maxMessages:        sessionEventReplayMaxMessages,
	maxCollectionItems: sessionEventReplayMaxCollectionItems,
}

func sessionReplayLimitError(path, resource string, value, limit int64) error {
	err := &SessionReplayLimitError{Path: path, Resource: resource, Value: value, Limit: limit}
	slog.Warn("session: refusing unsafe event-log replay",
		"path", path, "resource", resource, "value", value, "limit", limit)
	return err
}

type sessionEventIndex struct {
	SchemaVersion int       `json:"schema_version"`
	LogSize       int64     `json:"log_size"`
	MessageCount  int       `json:"message_count"`
	Revision      int64     `json:"revision"`
	ContentDigest string    `json:"content_digest"`
	WriterID      string    `json:"writer_id"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func SessionEventLogPath(sessionPath string) string {
	return store.SessionEventLog(sessionPath)
}

func SessionEventIndexPath(sessionPath string) string {
	return store.SessionEventIndex(sessionPath)
}

func sessionEventLogSize(sessionPath string) int64 {
	path := store.SessionEventLog(sessionPath)
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}

func sessionEventLogOversized(logSize, contentBytes int64) bool {
	limit := sessionEventLogCompactFloor
	if scaled := contentBytes * sessionEventLogCompactFactor; scaled > limit {
		limit = scaled
	}
	return logSize > limit
}

func decodeSessionEventMessages(
	ctx context.Context,
	path string,
	raw json.RawMessage,
	existingMessages, existingCollectionItems int,
	limits sessionReplayLimits,
) ([]provider.Message, int, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, existingCollectionItems, nil
	}
	messageCount, collectionItems, err := preflightSessionEventMessages(
		ctx, path, trimmed, existingMessages, existingCollectionItems, limits,
	)
	if err != nil {
		return nil, existingCollectionItems, err
	}
	dec := json.NewDecoder(&contextReader{ctx: ctx, reader: bytes.NewReader(trimmed)})
	tok, err := dec.Token()
	if err != nil {
		return nil, existingCollectionItems, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil, existingCollectionItems, fmt.Errorf("messages must be an array")
	}
	msgs := make([]provider.Message, 0, messageCount)
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return nil, existingCollectionItems, err
		}
		var msg provider.Message
		if err := dec.Decode(&msg); err != nil {
			return nil, existingCollectionItems, err
		}
		msgs = append(msgs, msg)
	}
	if _, err := dec.Token(); err != nil {
		return nil, existingCollectionItems, err
	}
	return msgs, collectionItems, nil
}

// preserveDamagedEventLogTail appends the about-to-be-truncated byte range of
// the event log to the .damaged salvage sidecar, prefixed with a one-line JSON
// header recording when and where the bytes came from. The sidecar is a
// forensic artifact for recovery, never replayed by the loader, and is removed
// with the session's other sidecars on delete.
func preserveDamagedEventLogTail(sessionPath, logPath string, from, to int64) error {
	if to <= from {
		return nil
	}
	src, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer src.Close()
	if _, err := src.Seek(from, io.SeekStart); err != nil {
		return err
	}
	dst, err := os.OpenFile(store.SessionEventLogDamaged(sessionPath), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("{\"damaged_tail\":true,\"preserved_at\":%q,\"log_offset\":%d,\"bytes\":%d}\n",
		time.Now().UTC().Format(time.RFC3339), from, to-from)
	if _, err := dst.WriteString(header); err != nil {
		dst.Close()
		return err
	}
	if _, err := io.CopyN(dst, src, to-from); err != nil && !errors.Is(err, io.EOF) {
		dst.Close()
		return err
	}
	if _, err := dst.WriteString("\n"); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

func readSessionEventIndex(sessionPath string) (*sessionEventIndex, error) {
	path := store.SessionEventIndex(sessionPath)
	if path == "" {
		return nil, nil
	}
	b, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return nil, err
	}
	var idx sessionEventIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}
	if idx.SchemaVersion != sessionEventSchemaVersion {
		return nil, fmt.Errorf("unsupported session event index schema %d", idx.SchemaVersion)
	}
	return &idx, nil
}

func writeSessionEventIndex(path string, msgs []provider.Message, digest [sha256.Size]byte, revision int64) error {
	return writeSessionEventIndexContext(context.Background(), path, msgs, digest, revision)
}

func writeSessionEventIndexContext(ctx context.Context, path string, msgs []provider.Message, digest [sha256.Size]byte, revision int64) error {
	indexPath := store.SessionEventIndex(path)
	if indexPath == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	logInfo, err := os.Stat(store.SessionEventLog(path))
	if err != nil {
		if os.IsNotExist(err) {
			// No log means nothing for the index to describe; drop a stale
			// index left by migration or manual sidecar cleanup.
			if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		return err
	}
	idx := sessionEventIndex{
		SchemaVersion: sessionEventSchemaVersion,
		LogSize:       logInfo.Size(),
		MessageCount:  len(msgs),
		Revision:      revision,
		ContentDigest: digestString(digest),
		WriterID:      SessionWriterID(),
		UpdatedAt:     time.Now().UTC(),
	}
	b, err := marshalJSONIndentContext(ctx, idx)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return atomicWriteFileContext(ctx, indexPath, ".session-event-index.*.tmp", "event-index", b, 0o600, false)
}
