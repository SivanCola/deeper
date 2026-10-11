package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"deepzora/internal/control"
	"deepzora/internal/session"
)

type sessionListEntry struct {
	HostID     string `json:"hostId,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Title      string `json:"title,omitempty"`
	Turns      int    `json:"turns,omitempty"`
	Current    bool   `json:"current,omitempty"`
	Running    bool   `json:"running,omitempty"`
	TakenOver  bool   `json:"takenOver,omitempty"`
	MtimeMilli int64  `json:"mtimeMilli"`

	Preview       string `json:"preview,omitempty"`
	MetadataReady bool   `json:"metadataReady,omitempty"`
	// A pointer distinguishes a supported zero baseline from legacy listings.
	ResultSequence *uint64 `json:"resultSequence,omitempty"`
}

// sessions lists current canonical sessions.
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	rows := s.canonicalSessionRows(r, s.ctl())
	out := make([]sessionListEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].MtimeMilli > out[j].MtimeMilli })
	writeJSON(w, out)
}

// readSessionDir reports ok=false only when the directory exists but cannot be
// read; a missing directory is an empty session list, not a failure.
func readSessionDir(dir string) ([]os.DirEntry, bool) {
	if dir == "" {
		return nil, true
	}
	entries, err := os.ReadDir(dir)
	switch {
	case os.IsNotExist(err):
		return nil, true
	case err != nil:
		return nil, false
	}
	return entries, true
}

func (s *Server) detachedRuntimeWork() map[string]bool {
	running := map[string]bool{}
	s.detachedMu.Lock()
	defer s.detachedMu.Unlock()
	for path, detached := range s.detached {
		running[filepath.Clean(path)] = controllerHasActiveRuntimeWork(detached.ctrl)
	}
	return running
}

// canonicalSessionRows follows the catalog cursor to the end. One List call
// caps at 100 rows ordered by the random session id, so a workspace past 100
// sessions would otherwise hide an arbitrary subset — including a session just
// taken over. The row bound keeps a broken cursor from looping forever.
func (s *Server) canonicalSessionRows(r *http.Request, ctrl control.SessionAPI) []canonicalSessionRow {
	rows := make([]canonicalSessionRow, 0)
	concrete, ok := ctrl.(*control.Controller)
	if !ok {
		return rows
	}
	service := concrete.SessionService()
	if service == nil {
		return rows
	}
	_, runtime, bound := concrete.SessionBinding()
	const pageLimit = 100
	const maxCanonicalRows = 500
	cursor := ""
	for pages := 1; ; pages++ {
		page, err := service.Query().List(r.Context(), cursor, pageLimit)
		if err != nil {
			break
		}
		if len(rows) == 0 {
			rows = make([]canonicalSessionRow, 0, len(page.Sessions))
		}
		for _, info := range page.Sessions {
			resultSequence := info.ResultSequence
			row := sessionListEntry{
				HostID: info.Ref.HostID, SessionID: info.Ref.SessionID, Name: info.SessionID,
				Title: info.Title, Turns: info.Turns, MtimeMilli: info.CreatedAt.UnixMilli(),
				Current:        bound && info.Ref == runtime.Ref(),
				Preview:        info.Preview,
				MetadataReady:  info.MetadataStatus == session.MetadataReady,
				ResultSequence: &resultSequence,
				TakenOver:      s.sessionMirrored(remoteSessionIDQueryPrefix + info.Ref.SessionID),
			}
			// Canonical rows carry no legacy preview fallback, so a chatted
			// session would list as untitled until the model renames it.
			if strings.TrimSpace(row.Title) == "" && strings.TrimSpace(info.Preview) != "" {
				row.Title = truncatedPreview(info.Preview)
			}
			if live, exists := service.Runtime(info.Ref); exists {
				row.Running = live.Snapshot().Phase.Busy()
			}
			rows = append(rows, canonicalSessionRow{row: row, info: info})
		}
		if page.NextCursor == "" || page.NextCursor == cursor || pages*pageLimit >= maxCanonicalRows || len(rows) >= maxCanonicalRows {
			break
		}
		cursor = page.NextCursor
	}
	return rows
}

// truncatedPreview clamps a catalog preview the way previewTitle clamps a
// transcript's first message. Catalog previews are already plain user text, so
// they need no paste-label stripping.
func truncatedPreview(preview string) string {
	preview = strings.TrimSpace(preview)
	if r := []rune(preview); len(r) > 50 {
		return string(r[:47]) + "..."
	}
	return preview
}

type canonicalSessionRow struct {
	row  sessionListEntry
	info session.SessionInfo
}
