package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

type historySessionOwner struct {
	workspaceID, root, version string
	contentPresent             bool
	generation                 string
}

// buildHistorySessionSnapshot freezes current metadata and ordering. It never
// opens a controller or reads a transcript to repair historical catalog rows.
func (a *App) buildHistorySessionSnapshot(ctx context.Context, store *readSnapshotStore, snap *readSnapshot, req HistorySessionPageRequest, active string) error {
	state, versions, err := a.workspaceRegistry().LoadProjectionWithVersions(ctx)
	if err != nil {
		return err
	}
	if _, err := historyWorkspaceOwners(state); err != nil {
		return err
	}
	query := a.desktopSessionService("").Query()
	overlays := a.catalogSessionRuntimeOverlays()
	owners := map[string]historySessionOwner{}
	rows := []SessionMeta{}
	meta := HistorySessionPage{Revision: state.Generation}
	now := time.Now()
	text := strings.ToLower(strings.TrimSpace(req.Query))
	for _, workspace := range state.Workspaces {
		scope := "project"
		if workspace.ID == workspacestate.GlobalWorkspaceID {
			scope = "global"
		}
		if req.Scope != "" && req.Scope != "all" && req.Scope != scope {
			continue
		}
		if req.WorkspaceRoot != "" && !sameProjectRoot(req.WorkspaceRoot, workspace.Root) {
			continue
		}
		for _, id := range workspace.SessionIDs {
			if state.SessionStates[id].Lifecycle != workspacestate.Active {
				continue
			}
			route := sessionRoute(id)
			overlay := overlays[sessionRuntimeKey(route)]
			if !historyStatusMatches(req.Status, overlay.open, route == active) {
				continue
			}
			ref := session.SessionRef{HostID: localDesktopHostID, SessionID: id}
			generation, generationErr := query.StorageGeneration(ctx, ref)
			info, err := query.Stat(ctx, ref)
			if err == nil {
				err = generationErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			contentPresent := err == nil
			if err != nil {
				info = session.SessionInfo{SessionID: id, Error: err.Error(), MetadataStatus: session.MetadataFailed}
			}
			presentation := state.Presentation[id]
			title, _ := sessionDisplayTitle(info, presentation)
			row := SessionMeta{Path: route, SessionID: id, HostID: localDesktopHostID, Codec: info.Codec,
				Preview: info.Preview, Title: title, Turns: info.Turns, TurnsState: "valid",
				CreatedAt: unixMillis(info.CreatedAt), LastActivityAt: unixMillis(info.UpdatedAt), Current: route == active,
				Open: overlay.open, Scope: scope, WorkspaceRoot: workspace.Root, TopicID: presentation.TopicID, TopicTitle: presentation.Title}
			if info.MetadataStatus != session.MetadataReady {
				row.TurnsState = "unknown"
				meta.Partial = true
			}
			if info.Error != "" {
				row.Error, row.TurnsState = info.Error, "corrupt"
			}
			if !historyTimeMatchesAt(row.LastActivityAt, req.TimeFilter, now) {
				continue
			}
			if text != "" && !strings.Contains(strings.ToLower(row.Title+"\n"+row.Preview+"\n"+row.TopicTitle), text) {
				continue
			}
			body, err := json.Marshal(row)
			if err != nil {
				return err
			}
			// Reserve the temporary sorting row and its lifecycle fence before
			// retaining it. The snapshot's ordinary quota also bounds this build.
			if err := store.reserve(snap, int64(len(body)+512)); err != nil {
				return err
			}
			rows = append(rows, row)
			owners[id] = historySessionOwner{workspaceID: workspace.ID, root: workspace.Root, version: versions.Session(id), contentPresent: contentPresent, generation: generation}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].LastActivityAt != rows[j].LastActivityAt {
			return rows[i].LastActivityAt > rows[j].LastActivityAt
		}
		return rows[i].SessionID < rows[j].SessionID
	})
	for _, row := range rows {
		if err := store.append(ctx, snap, row); err != nil {
			return err
		}
	}
	snap.validate = a.historySessionListFence(query, owners)
	snap.metadata, err = json.Marshal(meta)
	return err
}

func (a *App) historySessionListFence(query *session.Query, owners map[string]historySessionOwner) func() error {
	return func() error {
		current, err := a.workspaceRegistry().VerifySnapshot(a.bootContext())
		if err != nil {
			return err
		}
		for id, owner := range owners {
			member := current.Session(id)
			if member.OwnershipConflict || member.Workspace.ID != owner.workspaceID || member.Workspace.Root != owner.root || current.ReadVersions().Session(id) != owner.version {
				return snapshotStale("lifecycle_changed")
			}
			if !owner.contentPresent {
				continue
			}
			generation, err := query.StorageGeneration(a.bootContext(), session.SessionRef{HostID: localDesktopHostID, SessionID: id})
			if err != nil {
				if errors.Is(err, session.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) || errors.Is(err, session.ErrStaleGeneration) {
					return snapshotStale("lifecycle_changed")
				}
				return err
			}
			if generation != owner.generation {
				return snapshotStale("lifecycle_changed")
			}
		}
		return nil
	}
}
