// Package historycatalog maintains a disposable FTS projection of saved
// current session history. Framed session events remain authoritative.
package historycatalog

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/projectiondb"
	"deepzora/internal/session"
)

const (
	SchemaVersion    = 1
	TokenizerVersion = 1
	DefaultLimit     = 50
	MaxLimit         = 200
)

type Status struct {
	State           string            `json:"state"`
	Mode            projectiondb.Mode `json:"mode"`
	Path            string            `json:"path,omitempty"`
	Revision        uint64            `json:"revision"`
	Indexed         int64             `json:"indexed"`
	Total           int64             `json:"total"`
	Pending         int64             `json:"pending"`
	Failed          int64             `json:"failed"`
	LastError       string            `json:"lastError,omitempty"`
	QuarantinedPath string            `json:"quarantinedPath,omitempty"`
}

type Options struct {
	Path              string
	InMemory          bool
	QueueCapacity     int
	MissingGrace      time.Duration
	ReconcileInterval time.Duration
	MaxBytes          int64 // on-disk index cap; 0 = history_search.max_mb or DefaultMaxBytes
	Now               func() time.Time
	OnRevision        func(Status, []string, string)
}

type Root struct {
	// Query reads the current host catalog. It never acquires writer ownership.
	Query         *session.Query
	currentRefs   map[string]session.SessionRef
	Path          string
	Source        string
	Scope         string
	WorkspaceRoot string
}

type Candidate struct {
	RowID          int64
	SessionPath    string
	Root           string
	Source         string
	Scope          string
	WorkspaceRoot  string
	ContentDigest  string
	MessageIndex   int
	PartIndex      int
	Role           string
	Kind           string
	ToolName       string
	Rank           float64
	Score          float64
	SessionTitle   string
	TopicTitle     string
	LastActivityAt int64
}

type SearchRequest struct {
	Query         string
	Scope         string
	WorkspaceRoot string
	// SessionPath restricts results to one exact durable transcript. It is used
	// by target-bound sidebar previews so a failed/empty lookup can never fall
	// through to a different session in the same root.
	SessionPath string
	// Paths is a host-authorized selection. Non-nil empty grants no sources.
	Paths    []string
	Kinds    []string
	ToolName string
	Limit    int
	Roots    []string
	After    *SearchCursor
}

// SearchCursor is a stable keyset over SQLite's BM25 rank and deterministic
// tie-break keys. Lower BM25 ranks sort first.
type SearchCursor struct {
	Rank         float64
	SessionPath  string
	MessageIndex int
	PartIndex    int
	RowID        int64
}

type SearchResult struct {
	Items    []Candidate
	Revision uint64
	Partial  bool
}

// DefaultPath returns the disposable history FTS path under CacheDir.
// Empty when the OS cache directory is unavailable so Open falls back to memory.
func DefaultPath() string {
	cache := strings.TrimSpace(config.CacheDir())
	if cache == "" {
		return ""
	}
	return filepath.Join(cache, "history-search", "v1.sqlite")
}

// A path labels a source; only the current query grants read access to it.
func (r Root) validate() error {
	if r.Query == nil || strings.TrimSpace(r.Path) == "" || filepath.Clean(r.Path) != filepath.Clean(r.Query.StorageRoot()) {
		return errors.New("history: current session query required for catalog root")
	}
	return nil
}
