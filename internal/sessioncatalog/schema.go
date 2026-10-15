package sessioncatalog

import (
	"context"
	"database/sql"

	"deepzora/internal/projectiondb"
)

// currentSchema creates the complete disposable session projection. Cache
// generations are never upgraded: a schema change moves DefaultPath to a new
// file and authoritative transcripts rebuild every row.
const currentSchema = `
CREATE TABLE IF NOT EXISTS catalog_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO catalog_state(id, revision) VALUES(1, 0);

CREATE TABLE IF NOT EXISTS catalog_directories (
    path TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    workspace_root TEXT NOT NULL DEFAULT '',
    signature TEXT NOT NULL DEFAULT '',
    scan_cursor TEXT NOT NULL DEFAULT '',
    scan_generation INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    indexed INTEGER NOT NULL DEFAULT 0,
    total INTEGER NOT NULL DEFAULT 0,
    completed_at INTEGER NOT NULL DEFAULT 0,
    path_key TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS catalog_projects (
    scope TEXT NOT NULL,
    workspace_root TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    color TEXT NOT NULL DEFAULT '',
    pinned INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    workspace_root_key TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(scope, workspace_root)
);

CREATE TABLE IF NOT EXISTS catalog_topics (
    scope TEXT NOT NULL,
    workspace_root TEXT NOT NULL DEFAULT '',
    topic_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    title_source TEXT NOT NULL DEFAULT '',
    pinned INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    turns INTEGER NOT NULL DEFAULT 0,
    turns_state TEXT NOT NULL DEFAULT 'unknown',
    created_at INTEGER NOT NULL DEFAULT 0,
    last_activity_at INTEGER NOT NULL DEFAULT 0,
    recovery_state TEXT NOT NULL DEFAULT '',
    health TEXT NOT NULL DEFAULT 'ok',
    metadata_present INTEGER NOT NULL DEFAULT 0,
    recovery_branch_count INTEGER NOT NULL DEFAULT 0,
    recovery_unresolved_count INTEGER NOT NULL DEFAULT 0,
    recovery_cleanup_eligible_count INTEGER NOT NULL DEFAULT 0,
    workspace_root_key TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(scope, workspace_root, topic_id)
);

CREATE TABLE IF NOT EXISTS catalog_sessions (
    path TEXT PRIMARY KEY,
    directory TEXT NOT NULL,
    scope TEXT NOT NULL,
    workspace_root TEXT NOT NULL DEFAULT '',
    topic_id TEXT NOT NULL DEFAULT '',
    topic_title TEXT NOT NULL DEFAULT '',
    custom_title TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0,
    last_activity_at INTEGER NOT NULL DEFAULT 0,
    preview TEXT NOT NULL DEFAULT '',
    turns INTEGER NOT NULL DEFAULT 0,
    turns_state TEXT NOT NULL DEFAULT 'unknown',
    recovered INTEGER NOT NULL DEFAULT 0,
    recovery_reason TEXT NOT NULL DEFAULT '',
    recovery_digest TEXT NOT NULL DEFAULT '',
    parent_id TEXT NOT NULL DEFAULT '',
    content_fingerprint TEXT NOT NULL DEFAULT '',
    meta_fingerprint TEXT NOT NULL DEFAULT '',
    health TEXT NOT NULL DEFAULT 'ok',
    missing_since INTEGER NOT NULL DEFAULT 0,
    seen_generation INTEGER NOT NULL DEFAULT 0,
    recovery_copy INTEGER NOT NULL DEFAULT 0,
    recovery_group_id TEXT NOT NULL DEFAULT '',
    recovery_role TEXT NOT NULL DEFAULT '',
    recovery_canonical INTEGER NOT NULL DEFAULT 0,
    logical_topic_id TEXT NOT NULL DEFAULT '',
    ordinary_visible INTEGER NOT NULL DEFAULT 0,
    path_key TEXT NOT NULL DEFAULT '',
    directory_key TEXT NOT NULL DEFAULT '',
    workspace_root_key TEXT NOT NULL DEFAULT '',
    repair_state TEXT NOT NULL DEFAULT 'pending',
    repair_attempts INTEGER NOT NULL DEFAULT 0,
    repair_retry_at INTEGER NOT NULL DEFAULT 0,
    repair_error_kind TEXT NOT NULL DEFAULT '',
    repair_source_fingerprint TEXT NOT NULL DEFAULT '',
    repair_engine_version INTEGER NOT NULL DEFAULT 0,
    log_format INTEGER NOT NULL DEFAULT 1,
    head_count INTEGER NOT NULL DEFAULT 0,
    selected_head_id TEXT NOT NULL DEFAULT '',
    topic_pinned INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS catalog_folded_topics (
    scope TEXT NOT NULL,
    workspace_root TEXT NOT NULL DEFAULT '',
    topic_id TEXT NOT NULL,
    folded_at INTEGER NOT NULL DEFAULT 0,
    workspace_root_key TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(scope, workspace_root, topic_id)
);

CREATE TABLE IF NOT EXISTS catalog_heads (
    path_key TEXT NOT NULL,
    head_id TEXT NOT NULL,
    parent_head_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'main',
    name TEXT NOT NULL DEFAULT '',
    leaf_message_id TEXT NOT NULL DEFAULT '',
    writer_id TEXT NOT NULL DEFAULT '',
    last_activity_at INTEGER NOT NULL DEFAULT 0,
    turns INTEGER NOT NULL DEFAULT 0,
    preview TEXT NOT NULL DEFAULT '',
    retired INTEGER NOT NULL DEFAULT 0,
    selected INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(path_key, head_id)
);

CREATE TABLE IF NOT EXISTS catalog_pending_roots (
    path_key TEXT PRIMARY KEY,
    path TEXT NOT NULL,
    scope TEXT NOT NULL,
    workspace_root TEXT NOT NULL,
    sequence INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_catalog_topics_page
    ON catalog_topics(scope, workspace_root, pinned DESC, last_activity_at DESC, topic_id ASC);
CREATE INDEX IF NOT EXISTS idx_catalog_topics_activity
    ON catalog_topics(last_activity_at DESC, topic_id ASC);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_topic
    ON catalog_sessions(scope, workspace_root, topic_id, last_activity_at DESC, path ASC);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_directory
    ON catalog_sessions(directory, seen_generation, missing_since);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_path
    ON catalog_sessions(path);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_history
    ON catalog_sessions(scope, workspace_root, last_activity_at DESC, path ASC);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_recovery_group
    ON catalog_sessions(recovery_group_id, recovery_role, last_activity_at DESC);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_ordinary
    ON catalog_sessions(scope, workspace_root, ordinary_visible, last_activity_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_directories_path_key
    ON catalog_directories(path_key);
CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_sessions_path_key
    ON catalog_sessions(path_key);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_directory_key
    ON catalog_sessions(directory_key, seen_generation, missing_since);
CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_projects_workspace_key
    ON catalog_projects(scope, workspace_root_key);
CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_topics_workspace_key
    ON catalog_topics(scope, workspace_root_key, topic_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_folded_topics_workspace_key
    ON catalog_folded_topics(scope, workspace_root_key, topic_id);
CREATE INDEX IF NOT EXISTS idx_catalog_topics_workspace_page
    ON catalog_topics(scope, workspace_root_key, pinned DESC, last_activity_at DESC, topic_id ASC);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_workspace_topic
    ON catalog_sessions(scope, workspace_root_key, topic_id, last_activity_at DESC, path ASC);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_workspace_history
    ON catalog_sessions(scope, workspace_root_key, last_activity_at DESC, path ASC);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_workspace_ordinary
    ON catalog_sessions(scope, workspace_root_key, ordinary_visible, last_activity_at DESC);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_repair_due
    ON catalog_sessions(repair_state, repair_retry_at, last_activity_at DESC, path_key);
CREATE INDEX IF NOT EXISTS idx_catalog_heads_activity
    ON catalog_heads(path_key, retired, last_activity_at DESC);
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_flat_activity
    ON catalog_sessions(scope,workspace_root_key,topic_pinned DESC,COALESCE(NULLIF(last_activity_at,0),created_at) DESC,topic_id,path)
    WHERE ordinary_visible=1 AND missing_since=0 AND health<>'missing';
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_flat_created
    ON catalog_sessions(scope,workspace_root_key,topic_pinned DESC,COALESCE(NULLIF(created_at,0),last_activity_at) DESC,topic_id,path)
    WHERE ordinary_visible=1 AND missing_since=0 AND health<>'missing';
CREATE INDEX IF NOT EXISTS idx_catalog_sessions_multihead
    ON catalog_sessions(scope,workspace_root_key) WHERE head_count>1;

CREATE TRIGGER IF NOT EXISTS catalog_session_insert_pin AFTER INSERT ON catalog_sessions BEGIN
    UPDATE catalog_sessions SET topic_pinned=COALESCE((SELECT pinned FROM catalog_topics t WHERE t.scope=NEW.scope AND t.workspace_root_key=NEW.workspace_root_key AND t.topic_id=NEW.topic_id),0) WHERE path=NEW.path;
END;
CREATE TRIGGER IF NOT EXISTS catalog_session_move_pin AFTER UPDATE OF scope,workspace_root_key,topic_id ON catalog_sessions WHEN NEW.scope<>OLD.scope OR NEW.workspace_root_key<>OLD.workspace_root_key OR NEW.topic_id<>OLD.topic_id BEGIN
    UPDATE catalog_sessions SET topic_pinned=COALESCE((SELECT pinned FROM catalog_topics t WHERE t.scope=NEW.scope AND t.workspace_root_key=NEW.workspace_root_key AND t.topic_id=NEW.topic_id),0) WHERE path=NEW.path;
END;
CREATE TRIGGER IF NOT EXISTS catalog_topic_update_pin AFTER UPDATE OF pinned ON catalog_topics WHEN NEW.pinned<>OLD.pinned BEGIN
    UPDATE catalog_sessions SET topic_pinned=NEW.pinned WHERE scope=NEW.scope AND workspace_root_key=NEW.workspace_root_key AND topic_id=NEW.topic_id;
END;
CREATE TRIGGER IF NOT EXISTS catalog_topic_insert_pin AFTER INSERT ON catalog_topics WHEN NEW.pinned<>0 BEGIN
    UPDATE catalog_sessions SET topic_pinned=NEW.pinned WHERE scope=NEW.scope AND workspace_root_key=NEW.workspace_root_key AND topic_id=NEW.topic_id;
END;
`

func sessionSchema() []projectiondb.Migration {
	return []projectiondb.Migration{{Version: SchemaVersion, Apply: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, currentSchema)
		return err
	}}}
}
