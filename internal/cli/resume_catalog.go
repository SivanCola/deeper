package cli

import (
	"context"
	"sort"
	"strings"

	"deepzora/internal/agent"
	"deepzora/internal/session"
)

// canonicalResumeScanCap bounds how many final-format catalog rows one resume
// surface offers after ranking by recency. It matches the Serve-side listing
// page so both surfaces see the same conversation universe even on long-lived
// workspaces.
const canonicalResumeScanCap = 100

// canonicalResumeWalkCap bounds how many catalog rows one listing walks before
// ranking. The catalog pages in session-id order, not recency, so the newest
// conversation can sit on the last page; stopping after the first page hid it
// from the picker and from --continue. The cap keeps a pathological store from
// turning every /resume into an unbounded directory scan; rows beyond it are
// the lexically largest ids, not the newest activity.
const canonicalResumeWalkCap = 20 * canonicalResumeScanCap

// cliResumeTarget is one resumable conversation: either a legacy transcript
// path or a final-format session identity. Exactly one side is set.
type cliResumeTarget struct {
	path string             // legacy .jsonl transcript
	ref  session.SessionRef // sessions-v4 identity (SessionID != "" when canonical)
}

func (t cliResumeTarget) canonical() bool { return t.ref.SessionID != "" }

func (t cliResumeTarget) empty() bool { return t.path == "" && t.ref.SessionID == "" }

func (t cliResumeTarget) presetSource() *session.SessionRef {
	if !t.canonical() {
		return nil
	}
	return &t.ref
}

// canonicalCatalogLister is the catalog paging surface canonicalResumeEntries
// walks; *session.Query implements it. Tests page a synthetic catalog through
// the same code without building hundreds of on-disk sessions.
type canonicalCatalogLister interface {
	List(ctx context.Context, cursor string, limit int) (session.SessionPage, error)
}

// canonicalResumeEntries lists final-format (sessions-v4) sessions sharing the
// workspace of the legacy session dir. The catalog is the authoritative store
// once a legacy transcript has been imported, so every resume surface must
// offer these rows or switching between the desktop and the CLI hides history.
func canonicalResumeEntries(ctx context.Context, sessionDir string) []resumeEntry {
	service := cliSessionService(sessionDir)
	if service == nil {
		return nil
	}
	return canonicalResumeEntriesFrom(ctx, service.Query())
}

// canonicalResumeEntriesFrom walks the whole catalog (up to
// canonicalResumeWalkCap rows) before sorting by recency and applying the
// display cap, so the newest conversation is offered regardless of where its
// id sorts.
func canonicalResumeEntriesFrom(ctx context.Context, catalog canonicalCatalogLister) []resumeEntry {
	var out []resumeEntry
	cursor := ""
	for walked := 0; walked < canonicalResumeWalkCap; {
		page, err := catalog.List(ctx, cursor, canonicalResumeScanCap)
		if err != nil {
			break
		}
		walked += len(page.Sessions)
		for _, info := range page.Sessions {
			if canonicalResumeHidden(info) {
				continue
			}
			out = append(out, resumeEntry{
				session: canonicalResumeDisplayInfo(info),
				target:  cliResumeTarget{ref: info.Ref},
			})
		}
		if page.NextCursor == "" || page.NextCursor == cursor || len(page.Sessions) == 0 {
			break
		}
		cursor = page.NextCursor
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].session.LastActivityAt.After(out[j].session.LastActivityAt) })
	if len(out) > canonicalResumeScanCap {
		out = out[:canonicalResumeScanCap]
	}
	return out
}

// canonicalResumeHidden mirrors the legacy picker's empty-session rule for
// catalog rows: a session that never saw a user message (no completed turn,
// no preview, no title) is an empty placeholder and stays out of the picker.
// Rows whose metadata has not been rebuilt yet stay listed — an unindexed
// conversation must remain reachable, matching the desktop tree.
func canonicalResumeHidden(info session.SessionInfo) bool {
	if info.MetadataStatus != session.MetadataReady {
		return false
	}
	return info.Turns == 0 && strings.TrimSpace(info.Preview) == "" && strings.TrimSpace(info.Title) == ""
}

// canonicalResumeDisplayInfo projects a catalog row onto the picker's legacy
// row shape: the v4 directory stands in for the transcript path, the event-log
// revision time drives recency ordering, and a model-set catalog title rides
// the custom-title slot so sessionPickerLabel prefers it over the preview.
func canonicalResumeDisplayInfo(info session.SessionInfo) agent.SessionInfo {
	return agent.SessionInfo{
		Path: info.Path, Preview: strings.TrimSpace(info.Preview),
		CustomTitle: strings.TrimSpace(info.Title), Turns: info.Turns,
		CreatedAt: info.CreatedAt, LastActivityAt: info.UpdatedAt, CountsKnown: true,
	}
}

// mergedResumeEntries returns current canonical identities ranked by activity.
func mergedResumeEntries(sessionDir string, limit int) []resumeEntry {
	entries := canonicalResumeEntries(context.Background(), sessionDir)
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func newestResumeTarget(sessionDir string) (cliResumeTarget, bool) {
	entries := mergedResumeEntries(sessionDir, 1)
	if len(entries) == 0 {
		return cliResumeTarget{}, false
	}
	return entries[0].target, true
}

func foreignProjectResumeRows(sessionDir string) []agent.SessionInfo {
	entries := canonicalResumeEntries(context.Background(), sessionDir)
	rows := make([]agent.SessionInfo, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, entry.session)
	}
	return rows
}
