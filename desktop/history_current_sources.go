package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/agent"
	"deepzora/internal/historycatalog"
	"deepzora/internal/session"
	"deepzora/internal/subagentpersist"
)

const (
	historySessionCatalog  = "sessions"
	historySubagentCatalog = "subagents"
)

// Search addresses include a catalog because a parent and a child can have the
// same opaque ID. Paths are private index keys, never a caller's read authority.
type historyCurrentSource struct {
	query                                                      *session.Query
	info                                                       session.SessionInfo
	catalog, parentID, scope, workspaceRoot, title, topicTitle string
	owner                                                      historySessionOwner
}

func (a *App) currentHistoryCatalogRoots() ([]historycatalog.Root, error) {
	query := a.desktopSessionService("").Query()
	child, err := query.Subagents()
	if err != nil {
		return nil, err
	}
	return []historycatalog.Root{historycatalog.CurrentRoot(query), historycatalog.CurrentRoot(child)}, nil
}

func historySearchScopeMatches(req HistorySearchRequest, scope, root string) bool {
	if req.Scope == "project" {
		return scope == "project" && sameProjectRoot(req.WorkspaceRoot, root)
	}
	if req.Scope == "global" {
		return scope == "global" || scope == "archive"
	}
	return req.WorkspaceRoot == "" || sameProjectRoot(req.WorkspaceRoot, root)
}

func historyWorkspaceOwners(state workspacestate.State) (map[string]workspacestate.Workspace, error) {
	owners := map[string]workspacestate.Workspace{}
	for _, workspace := range state.Workspaces {
		for _, id := range workspace.SessionIDs {
			if _, exists := owners[id]; exists {
				return nil, errors.New("history: ambiguous session ownership")
			}
			owners[id] = workspace
		}
	}
	return owners, nil
}

func (a *App) currentHistorySources(ctx context.Context, req HistorySearchRequest, admit func(historyCurrentSource) error) (map[string]historyCurrentSource, error) {
	state, versions, err := a.workspaceRegistry().LoadProjectionWithVersions(ctx)
	if err != nil {
		return nil, err
	}
	owners, err := historyWorkspaceOwners(state)
	if err != nil {
		return nil, err
	}
	query := a.desktopSessionService("").Query()
	entries, parents := map[string]historyCurrentSource{}, map[string]historyCurrentSource{}
	for id, lifecycle := range state.SessionStates {
		workspace, registered := owners[id]
		scope := "project"
		if workspace.ID == workspacestate.GlobalWorkspaceID {
			scope = "global"
		}
		if lifecycle.Lifecycle == workspacestate.Archived {
			scope = "archive"
		} else if lifecycle.Lifecycle != workspacestate.Active || !registered {
			continue
		}
		if !historySearchScopeMatches(req, scope, workspace.Root) {
			continue
		}
		info, err := query.Stat(ctx, session.SessionRef{HostID: localDesktopHostID, SessionID: id})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, session.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Error != "" {
			return nil, fmt.Errorf("history: session %s: %s", id, info.Error)
		}
		presentation := state.Presentation[id]
		title, _ := sessionDisplayTitle(info, presentation)
		entry := historyCurrentSource{query: query, info: info, catalog: historySessionCatalog, parentID: id,
			scope: scope, workspaceRoot: workspace.Root, title: title, topicTitle: presentation.Title,
			owner: historySessionOwner{workspaceID: workspace.ID, root: workspace.Root, version: versions.Session(id), contentPresent: true}}
		if admit != nil {
			if err := admit(entry); err != nil {
				return nil, err
			}
		}
		entries[info.Path], parents[id] = entry, entry
	}
	children, err := query.Subagents()
	if err != nil {
		return nil, err
	}
	artifacts, err := agent.ListSubagentArtifacts(children.StorageRoot())
	if err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		parent, allowed := parents[artifact.Meta.ParentSession]
		if !allowed {
			continue
		}
		info, err := children.Stat(ctx, session.SessionRef{HostID: localDesktopHostID, SessionID: artifact.Ref})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, session.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Error != "" {
			return nil, fmt.Errorf("history: child %s: %s", artifact.Ref, info.Error)
		}
		entry := parent
		entry.query, entry.info, entry.catalog = children, info, historySubagentCatalog
		entry.title = info.Title
		if admit != nil {
			if err := admit(entry); err != nil {
				return nil, err
			}
		}
		entries[info.Path] = entry
	}
	return entries, nil
}

type historyCurrentFence struct {
	app         *App
	entries     map[string]historyCurrentSource
	generations map[string]string
}

func (f *historyCurrentFence) validate() error {
	current, err := f.app.workspaceRegistry().VerifySnapshot(f.app.bootContext())
	if err != nil {
		return err
	}
	var children *agent.SubagentStore
	for path, entry := range f.entries {
		member := current.Session(entry.parentID)
		if member.OwnershipConflict || member.Workspace.ID != entry.owner.workspaceID || member.Workspace.Root != entry.owner.root || current.ReadVersions().Session(entry.parentID) != entry.owner.version {
			return snapshotStale("lifecycle_changed")
		}
		if entry.catalog == historySubagentCatalog {
			if children == nil {
				service, err := f.app.desktopSessionService("").Subagents()
				if err != nil {
					return err
				}
				children = agent.NewSubagentStore(subagentpersist.New(service))
			}
			meta, err := children.LoadMeta(entry.info.SessionID)
			if err != nil || meta.ParentSession != entry.parentID {
				return snapshotStale("lifecycle_changed")
			}
			if _, err := f.app.desktopSessionService("").Query().Stat(f.app.bootContext(), session.SessionRef{HostID: localDesktopHostID, SessionID: entry.parentID}); err != nil {
				return snapshotStale("lifecycle_changed")
			}
		}
		generation, err := entry.query.StorageGeneration(f.app.bootContext(), entry.info.Ref)
		if errors.Is(err, session.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) || errors.Is(err, session.ErrStaleGeneration) {
			return snapshotStale("lifecycle_changed")
		}
		if err != nil {
			return err
		}
		if generation != f.generations[path] {
			return snapshotStale("lifecycle_changed")
		}
	}
	return nil
}

func (a *App) historyContextSource(ctx context.Context, catalog, id string) (historyCurrentSource, bool) {
	if strings.TrimSpace(id) == "" || (catalog != historySessionCatalog && catalog != historySubagentCatalog) {
		return historyCurrentSource{}, false
	}
	parentQuery := a.desktopSessionService("").Query()
	query, parentID := parentQuery, id
	if catalog == historySubagentCatalog {
		children, err := a.desktopSessionService("").Subagents()
		if err != nil {
			return historyCurrentSource{}, false
		}
		meta, err := agent.NewSubagentStore(subagentpersist.New(children)).LoadMeta(id)
		if err != nil {
			return historyCurrentSource{}, false
		}
		query, parentID = children.Query(), meta.ParentSession
	}
	current, err := a.workspaceRegistry().VerifySnapshot(ctx)
	if err != nil {
		return historyCurrentSource{}, false
	}
	member := current.Session(parentID)
	if !member.Registered || member.OwnershipConflict {
		return historyCurrentSource{}, false
	}
	if member.State.Lifecycle != workspacestate.Archived && (member.State.Lifecycle != workspacestate.Active || member.Workspace.ID == "") {
		return historyCurrentSource{}, false
	}
	if catalog == historySubagentCatalog {
		if _, err := parentQuery.Stat(ctx, session.SessionRef{HostID: localDesktopHostID, SessionID: parentID}); err != nil {
			return historyCurrentSource{}, false
		}
	}
	info, err := query.Stat(ctx, session.SessionRef{HostID: localDesktopHostID, SessionID: id})
	if err != nil || info.Error != "" {
		return historyCurrentSource{}, false
	}
	return historyCurrentSource{query: query, info: info, catalog: catalog, parentID: parentID,
		owner: historySessionOwner{workspaceID: member.Workspace.ID, root: member.Workspace.Root, version: current.ReadVersions().Session(parentID), contentPresent: true}}, true
}
