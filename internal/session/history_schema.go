package session

import (
	"context"
	"database/sql"

	"deepzora/internal/projectiondb"
)

// The disposable index starts with the complete current schema. It never
// upgrades an older cache, and readers never perform compatibility DDL.
var historySchema = []projectiondb.Migration{{Version: 1, Apply: func(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE transactions (commit_id TEXT PRIMARY KEY, first_sequence INTEGER NOT NULL, last_sequence INTEGER NOT NULL, operation_id TEXT NOT NULL UNIQUE, operation_hash TEXT NOT NULL, turn_id TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE events (sequence INTEGER PRIMARY KEY, commit_id TEXT NOT NULL, event_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, payload_digest TEXT NOT NULL DEFAULT '', payload_bytes INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE messages (message_id TEXT NOT NULL, version INTEGER NOT NULL, position INTEGER NOT NULL, event_sequence INTEGER NOT NULL, valid_to INTEGER NOT NULL DEFAULT 0, role TEXT NOT NULL, preview TEXT NOT NULL, inline BLOB, content_digest TEXT NOT NULL DEFAULT '', content_bytes INTEGER NOT NULL DEFAULT 0, content_index_digest TEXT NOT NULL DEFAULT '', current INTEGER NOT NULL, search_text TEXT NOT NULL DEFAULT '', visible_turn INTEGER NOT NULL DEFAULT 0, visible_user INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(message_id, version))`,
		`CREATE UNIQUE INDEX messages_current_position ON messages(position) WHERE current=1`,
		`CREATE INDEX messages_current_id ON messages(message_id) WHERE current=1`,
		`CREATE INDEX messages_snapshot_position ON messages(position DESC, event_sequence, valid_to)`,
		`CREATE INDEX messages_outline_users ON messages(visible_user,visible_turn,position,event_sequence,valid_to)`,
		`CREATE INDEX messages_outline_answers ON messages(visible_turn,role,position DESC,event_sequence,valid_to)`,
		`CREATE TABLE content_refs (digest TEXT NOT NULL, bytes INTEGER NOT NULL, index_digest TEXT NOT NULL DEFAULT '', PRIMARY KEY(digest, bytes, index_digest))`,
		`CREATE TABLE turn_summaries (turn_id TEXT PRIMARY KEY, start_sequence INTEGER NOT NULL DEFAULT 0, end_sequence INTEGER NOT NULL DEFAULT 0, started_at INTEGER NOT NULL DEFAULT 0, ended_at INTEGER NOT NULL DEFAULT 0, final_message_id TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX turn_summaries_final ON turn_summaries(final_message_id)`,
		`CREATE TABLE turn_counts (turn_id TEXT NOT NULL, kind TEXT NOT NULL, id TEXT NOT NULL, sequence INTEGER NOT NULL, PRIMARY KEY(turn_id,kind,id))`,
		`CREATE TABLE submissions (session_id TEXT NOT NULL, submission_id TEXT NOT NULL, message_id TEXT NOT NULL, sequence INTEGER NOT NULL, PRIMARY KEY(session_id,submission_id))`,
		`CREATE INDEX submissions_message ON submissions(message_id)`,
		`CREATE TABLE tool_links (message_id TEXT NOT NULL,digest TEXT NOT NULL,call_id TEXT NOT NULL,is_result INTEGER NOT NULL,state TEXT NOT NULL,PRIMARY KEY(message_id,digest,call_id,is_result))`,
		`CREATE INDEX tool_links_call ON tool_links(call_id,is_result)`,
		`CREATE TABLE tool_states (call_id TEXT NOT NULL,sequence INTEGER NOT NULL,state TEXT NOT NULL,PRIMARY KEY(call_id,sequence))`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}}}
