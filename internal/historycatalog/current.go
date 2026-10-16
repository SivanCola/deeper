package historycatalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

// CurrentRoot indexes the supplied authoritative catalog. Host membership is
// applied by the reader, not persisted in this disposable shared projection.
func CurrentRoot(query *session.Query) Root {
	return Root{Query: query, Path: query.StorageRoot(), Source: "global", Scope: "global"}
}

// CoversCurrent distinguishes an empty match from a source whose newest
// committed projection has not reached the asynchronous index yet.
// Missing revisions are queued through their registered current catalog, so a
// new message with no older matching terms does not wait for a periodic scan.
func (c *Catalog) CoversCurrent(ctx context.Context, infos []session.SessionInfo) (bool, error) {
	paths := make([]string, 0, len(infos))
	expected := make(map[string]uint64, len(infos))
	for _, info := range infos {
		paths = append(paths, info.Path)
		expected[info.Path] = info.EventSequence
	}
	encoded, err := json.Marshal(paths)
	if err != nil {
		return false, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT path,content_revision FROM history_sources WHERE health='ok' AND missing_since=0 AND path IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		var revision uint64
		if err := rows.Scan(&path, &revision); err != nil {
			return false, err
		}
		if expected[path] == revision {
			delete(expected, path)
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for path := range expected {
		c.mu.Lock()
		root, registered := c.roots[filepath.Dir(path)]
		c.mu.Unlock()
		if registered && root.Query != nil {
			c.EnqueuePath(root, path)
		}
	}
	return len(expected) == 0, nil
}

func currentPaths(ctx context.Context, root *Root) ([]string, error) {
	paths := []string{}
	root.currentRefs = map[string]session.SessionRef{}
	for cursor := ""; ; {
		page, err := root.Query.List(ctx, cursor, 100)
		if err != nil {
			return nil, err
		}
		for _, info := range page.Sessions {
			if info.Path != "" && catalogPathWithin(info.Path, root.Path) {
				paths = append(paths, info.Path)
				root.currentRefs[info.Path] = info.Ref
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
	sort.Strings(paths)
	return paths, nil
}

// ReadCurrent reads one catalog-observed identity, never a transcript path.
// The digest fences positional search hits against replacement/rewind between
// indexing and rendering. It is computed from the same snapshot as the body.
func ReadCurrent(ctx context.Context, query *session.Query, ref session.SessionRef) ([]provider.Message, string, session.SessionInfo, error) {
	info, err := query.Stat(ctx, ref)
	if err != nil {
		return nil, "", info, err
	}
	snapshot, err := query.Snapshot(ctx, ref)
	if err != nil {
		return nil, "", info, err
	}
	messages := snapshot.Projection.Messages
	body, err := json.Marshal(struct {
		Sequence uint64
		Messages []provider.Message
	}{snapshot.EventSequence, messages})
	if err != nil {
		return nil, "", info, err
	}
	digest := sha256.Sum256(body)
	info.EventSequence = snapshot.EventSequence
	return messages, hex.EncodeToString(digest[:]), info, nil
}

func (c *Catalog) indexCurrentSession(ctx context.Context, root Root, path string, generation int64) error {
	if err := root.validate(); err != nil {
		return err
	}
	if filepath.Dir(path) != filepath.Clean(root.Path) {
		return fmt.Errorf("history: session outside catalog")
	}
	ref, observed := root.currentRefs[path]
	if !observed {
		var err error
		ref, err = root.Query.ResolveSessionID(ctx, filepath.Base(path))
		if errors.Is(err, session.ErrSessionNotFound) {
			return c.purge(ctx, path)
		}
		if err != nil {
			return err
		}
	}
	messages, digest, info, err := ReadCurrent(ctx, root.Query, ref)
	if errors.Is(err, session.ErrSessionNotFound) {
		return c.purge(ctx, path)
	}
	if err != nil {
		// Hide stale terms immediately; a later reconcile can retry the source.
		_, _ = c.db.ExecContext(ctx, `UPDATE history_sources SET health='corrupt',last_error=? WHERE path=?`, err.Error(), path)
		c.setError(err)
		return err
	}
	if filepath.Clean(info.Path) != path {
		return fmt.Errorf("history: catalog identity changed")
	}
	metadata, _ := json.Marshal(info)
	metaHash := sha256.Sum256(metadata)
	metaFingerprint := hex.EncodeToString(metaHash[:])
	var oldDigest, oldMeta string
	err = c.db.QueryRowContext(ctx, `SELECT content_digest,meta_fingerprint FROM history_sources WHERE path=?`, path).Scan(&oldDigest, &oldMeta)
	if err == nil && oldDigest == digest && oldMeta == metaFingerprint {
		_, err = c.db.ExecContext(ctx, `UPDATE history_sources SET seen_generation=CASE WHEN ?>0 THEN ? ELSE seen_generation END,
			missing_since=0,health=CASE WHEN health='evicted' THEN 'evicted' ELSE 'ok' END WHERE path=?`, generation, generation, path)
		return err
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return c.replaceSource(ctx, root, path, generation, int64(info.EventSequence), digest, metaFingerprint, messages,
		sourceMetadata{title: info.Title, preview: info.Preview, createdAt: info.CreatedAt.UnixMilli(), lastActivity: info.UpdatedAt.UnixMilli()})
}
