package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

// Both recovery and the sidebar resolve durable registry members. Legacy
// Current session identities are resolved through their workspace registry.
func (a *App) unifiedProjectTopics(req ProjectTopicPageRequest) (ProjectTopicPage, error) {
	return a.readProjectTopicPage(req, a.desktopSessionService("").Query())
}

func (a *App) readProjectTopicPage(req ProjectTopicPageRequest, reader workspaceSessionInfoReader) (ProjectTopicPage, error) {
	scope, root, err := normalizeOrganizationTarget(req.Scope, req.WorkspaceRoot)
	if err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, err
	}
	req.Scope, req.WorkspaceRoot = scope, root
	identity := req
	identity.Cursor, identity.Limit = "", 0
	binding := snapshotBinding("project-topics", []any{identity, req.pinnedOnly})
	store := &a.desktopSessions.readSnapshots
	var first *readSnapshot
	if req.Cursor == "" {
		first, err = store.build(a.bootContext(), binding, func(ctx context.Context, snap *readSnapshot) error {
			page, validate, err := a.buildProjectTopics(req, reader, snap)
			if err != nil {
				return err
			}
			snap.validate = validate
			for _, row := range page.Items {
				if err := store.reserve(snap, 512); err != nil {
					return err
				}
				if err := store.append(ctx, snap, row); err != nil {
					return err
				}
			}
			page.Items = nil
			snap.metadata, err = json.Marshal(page)
			return err
		})
		if err != nil {
			return ProjectTopicPage{Items: []ProjectNode{}}, err
		}
	}
	items := []ProjectNode{}
	next, id, expires, meta, err := store.page(a.bootContext(), binding, req.Cursor, first, req.Limit, func(b []byte) error {
		var row ProjectNode
		if err := json.Unmarshal(b, &row); err != nil {
			return err
		}
		items = append(items, row)
		return nil
	})
	if err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, err
	}
	var out ProjectTopicPage
	if err := json.Unmarshal(meta, &out); err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, err
	}
	out.Items, out.NextCursor, out.SnapshotID, out.SnapshotExpiresAt = items, next, id, expires
	return out, nil
}

func (a *App) buildProjectTopics(req ProjectTopicPageRequest, reader workspaceSessionInfoReader, snap *readSnapshot) (ProjectTopicPage, func() error, error) {
	scope, root := req.Scope, req.WorkspaceRoot
	workspaceID, _, err := a.ensureSessionOrganization(scope, root)
	if err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, nil, err
	}
	state, versions, err := a.workspaceRegistry().LoadProjectionWithVersions(a.bootContext())
	if err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, nil, err
	}
	workspace := state.Workspaces[workspaceID]
	org := workspacestate.Organization{}
	if workspace.Organization != nil {
		org = *workspace.Organization
	}

	return a.materializeProjectTopics(req, reader, snap, state, workspacestate.NewWorkspaceIndex(state), workspaceID, org, nil, versions)
}

// projectTopicsFromProjection materializes one workspace from a caller-owned
// registry snapshot. Project-tree reads use it without organization migration
// or repeated registry loads, so building sidebar shells remains read-only.
func (a *App) projectTopicsFromProjection(req ProjectTopicPageRequest, state workspacestate.State, workspaceIndex *workspacestate.WorkspaceIndex, workspaceID string, org workspacestate.Organization, shellPreferences *desktopProject, snapshotVersions ...*workspacestate.ReadVersions) (ProjectTopicPage, error) {
	scope, root, err := normalizeOrganizationTarget(req.Scope, req.WorkspaceRoot)
	if err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, err
	}
	req.Scope, req.WorkspaceRoot = scope, root
	identity := req
	identity.Cursor, identity.Limit = "", 0
	binding := snapshotBinding("project-topics-projection", []any{identity, req.pinnedOnly, state.Generation, org.Revision, shellPreferences})
	store := &a.desktopSessions.readSnapshots
	var first *readSnapshot
	if req.Cursor == "" {
		first, err = store.build(a.bootContext(), binding, func(ctx context.Context, snap *readSnapshot) error {
			versions := projectionReadVersions(state, snapshotVersions)
			page, validate, err := a.materializeProjectTopics(req, a.desktopSessionService("").Query(), snap, state, workspaceIndex, workspaceID, org, shellPreferences, versions)
			if err != nil {
				return err
			}
			snap.validate = validate
			for _, row := range page.Items {
				if err := store.reserve(snap, 512); err != nil {
					return err
				}
				if err := store.append(ctx, snap, row); err != nil {
					return err
				}
			}
			page.Items = nil
			snap.metadata, err = json.Marshal(page)
			return err
		})
		if err != nil {
			return ProjectTopicPage{Items: []ProjectNode{}}, err
		}
	}
	items := []ProjectNode{}
	next, id, expires, meta, err := store.page(a.bootContext(), binding, req.Cursor, first, req.Limit, func(b []byte) error {
		var row ProjectNode
		if err := json.Unmarshal(b, &row); err != nil {
			return err
		}
		items = append(items, row)
		return nil
	})
	if err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, err
	}
	var out ProjectTopicPage
	if err := json.Unmarshal(meta, &out); err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, err
	}
	out.Items, out.NextCursor, out.SnapshotID, out.SnapshotExpiresAt = items, next, id, expires
	return out, nil
}

func (a *App) materializeProjectTopics(req ProjectTopicPageRequest, reader workspaceSessionInfoReader, snap *readSnapshot, state workspacestate.State, workspaceIndex *workspacestate.WorkspaceIndex, workspaceID string, org workspacestate.Organization, shellPreferences *desktopProject, versions *workspacestate.ReadVersions) (ProjectTopicPage, func() error, error) {
	workspace := state.Workspaces[workspaceID]
	if err := applyOrganizationGroupFilter(&req, org); err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, nil, err
	}
	workspace.SessionIDs = admittedWorkspaceTopicMembers(req, state, workspace)
	infos, _ := listWorkspaceSessionInfo(a.bootContext(), reader, workspace.SessionIDs)
	sources := a.withRemovablePlaceholderTopics(req, state, nil, state.AdoptedTopicIDs(workspaceID))
	page := ProjectTopicPage{Items: []ProjectNode{}, Complete: true}
	filtered := a.indexedWorkspaceTopics(req, state, workspace, org, infos, sources)
	page.Items = cloneTopicPage(filtered)
	for i := range page.Items {
		if ref := page.Items[i].Session; ref != nil {
			_, page.Items[i].Open = a.desktopSessionService("").Runtime(*ref)
		}
	}
	page.Revision += state.Generation
	page.NextCursor = ""
	// Ordinary activity and organization edits do not revoke frozen reads.
	// Membership removal, lifecycle transitions and source adoption do.
	validate := a.workspaceReadFence(versions, workspace, filtered)
	sourcesFence := &readSourceFence{app: a, files: map[string]os.FileInfo{}, store: &a.desktopSessions.readSnapshots, snapshot: snap, metadataOnly: true}
	for _, node := range filtered {
		if node.Session != nil {
			continue
		}
		path := node.SessionPath

		if path != "" {
			if err := sourcesFence.add(a.bootContext(), path); err != nil {
				return page, nil, err
			}
		}
	}
	return page, func() error {
		current, err := a.workspaceRegistry().VerifySnapshot(a.bootContext())
		if err != nil {
			return err
		}
		if err := validate(current); err != nil {
			return err
		}
		return sourcesFence.validateWithCurrent(current)
	}, nil
}

func desktopSessionTimeCutoff(filter string) int64 {
	value := strings.ToLower(strings.TrimSpace(filter))
	switch value {
	case "day":
		value = "24h"
	case "week", "7d":
		value = "168h"
	case "month", "30d":
		value = "720h"
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0
	}
	return time.Now().Add(-duration).UnixMilli()
}

func projectTopicTimeCutoff(req ProjectTopicPageRequest) int64 {
	if req.timeCutoff > 0 {
		return req.timeCutoff
	}
	return desktopSessionTimeCutoff(req.TimeFilter)
}

func (a *App) unifiedProjectRevision(catalogRevision uint64) uint64 {
	state, err := a.workspaceRegistry().LoadProjection(a.bootContext())
	if err != nil {
		return catalogRevision
	}
	return catalogRevision + state.Generation
}

func (a *App) updateCanonicalTopicPresentation(topicID string, title *string, pinned *bool) (bool, error) {
	state, err := a.workspaceRegistry().LoadProjection(a.bootContext())
	if err != nil {
		return false, err
	}
	ids := []string{}
	for _, workspace := range state.Workspaces {
		for _, id := range workspace.SessionIDs {
			if state.Presentation[id].TopicID == topicID || "canonical-"+id == topicID {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return false, nil
	}
	if title != nil {
		for _, id := range ids {
			ref := session.SessionRef{HostID: localDesktopHostID, SessionID: id}
			if err := a.desktopSessionService("").SetTitle(a.bootContext(), ref, *title); err != nil {
				return true, err
			}
			a.publishCanonicalSessionTitle(ref, *title)
		}
	}
	if pinned != nil {
		if err := a.workspaceRegistry().UpdatePresentation(a.bootContext(), ids, nil, pinned); err != nil {
			return true, err
		}
		a.emitProjectTreeMetadataChanged()
	}
	return true, nil
}

func (a *App) mergeCanonicalWorkspaceShells(projects []ProjectNode) []ProjectNode {
	state, versions, err := a.workspaceRegistry().LoadProjectionWithVersions(a.bootContext())
	if err != nil {
		return projects
	}
	return a.mergeCanonicalWorkspaceShellsFromProjection(projects, state, versions)
}

func (a *App) mergeCanonicalWorkspaceShellsFromProjection(projects []ProjectNode, state workspacestate.State, snapshotVersions ...*workspacestate.ReadVersions) []ProjectNode {
	versions := projectionReadVersions(state, snapshotVersions)
	preferences := loadProjectsFile()
	workspaceIndex := workspacestate.NewWorkspaceIndex(state)
	owner := func(scope, root string) (string, bool) {
		if scope != "project" {
			_, found := state.Workspaces[workspacestate.GlobalWorkspaceID]
			return workspacestate.GlobalWorkspaceID, found
		}
		id, found, err := workspaceIndex.Resolve(root)
		return id, found && err == nil
	}
	present := map[string]bool{}
	visible := make([]ProjectNode, 0, len(projects))
	for _, project := range projects {
		if project.Remote != nil {
			visible = append(visible, project)
			continue
		}
		scope := "project"
		if project.Kind == "global_folder" {
			scope = "global"
		}
		id, found := owner(scope, project.Root)
		if workspace, ok := state.Workspaces[id]; found && ok && !workspace.Visible {
			continue
		}
		if found {
			present[id] = true
		}
		visible = append(visible, project)
	}
	projects = visible
	for _, id := range state.WorkspaceIDs {
		workspace := state.Workspaces[id]
		if !workspace.Visible || present[id] {
			continue
		}
		if id == workspacestate.GlobalWorkspaceID && !globalWorkspaceHasSessions(state) {
			continue
		}
		kind, key := "project", "project_"+workspace.Root
		if id == workspacestate.GlobalWorkspaceID {
			kind, key = "global_folder", "global_folder"
		}
		projects = append(projects, ProjectNode{Key: key, Kind: kind, Root: workspace.Root, Label: workspace.Title, Children: []ProjectNode{}})
	}
	// Pinned shells must use the same canonical rows as ordinary pages. Legacy
	// shells otherwise overwrite the title/key on refresh and resurrect pins
	// for sessions whose registry lifecycle is already archived.
	for index := range projects {
		project := &projects[index]
		if project.Remote != nil {
			continue
		}
		scope, root := "project", project.Root
		if project.Kind == "global_folder" {
			scope, root = "global", ""
		}
		req := ProjectTopicPageRequest{Scope: scope, WorkspaceRoot: root, Limit: 200, pinnedOnly: true}
		workspaceID, _ := owner(scope, root)
		preferencesForShell := projectShellPreferences(preferences, scope, root)

		pins := []ProjectNode{}
		snapshotID := ""
		for {
			page, err := a.projectTopicsFromProjection(req, state, workspaceIndex, workspaceID, workspacestate.Organization{}, &preferencesForShell, versions)
			if err != nil {
				project.Health = "metadata_failed"
				break
			}
			if req.Cursor == "" {
				snapshotID = page.SnapshotID
			}
			unpinned := false
			for _, node := range page.Items {
				if node.Pinned {
					pins = append(pins, node)
				} else {
					unpinned = true
				}
			}
			if unpinned || page.NextCursor == "" {
				project.Children = pins
				break
			}
			req.Cursor = page.NextCursor
		}
		a.ReleaseReadSnapshot(snapshotID)
	}
	return projects
}

// globalWorkspaceHasSessions decides whether the registry alone keeps Global
// in the sidebar. Global is never added by the user: every draft or session
// aimed at it marks it Visible, so Visible without a session is not a request.
func globalWorkspaceHasSessions(state workspacestate.State) bool {
	for _, sessionID := range state.Workspaces[workspacestate.GlobalWorkspaceID].SessionIDs {
		if lifecycle := state.SessionStates[sessionID].Lifecycle; lifecycle != workspacestate.Archived && lifecycle != workspacestate.Deleted {
			return true
		}
	}

	return false
}

func (a *App) canonicalTopicNodes(req ProjectTopicPageRequest, state workspacestate.State, workspace workspacestate.Workspace, infos map[string]session.SessionInfo, initial []ProjectNode, created ...map[string]int64) []ProjectNode {
	nodes := initial
	query := strings.ToLower(strings.TrimSpace(req.Query))
	cutoff := projectTopicTimeCutoff(req)
	var createdTopics map[string]int64
	if len(created) > 0 {
		createdTopics = created[0]
	} else {
		createdTopics = loadTopicCreatedAts(topicTitleRoot(req.Scope, req.WorkspaceRoot))
	}
	for index, id := range workspace.SessionIDs {
		if state.SessionStates[id].Lifecycle != workspacestate.Active {
			continue
		}
		info, found := infos[id]
		if !found || info.MetadataStatus == session.MetadataFailed {
			continue
		}
		node := a.canonicalProjectSessionNode(state, workspace, info, id, index, createdTopics)
		if cutoff > 0 && max(node.CreatedAt, node.LastActivityAt) < cutoff {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(node.Label+"\n"+node.Preview+"\n"+id), query) {
			continue
		}
		if projectNodeRequestAllows(req, node) {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func (a *App) canonicalProjectSessionNode(state workspacestate.State, workspace workspacestate.Workspace, info session.SessionInfo, id string, index int, createdTopics map[string]int64) ProjectNode {
	row := workspaceSessionRow(workspace.ID, id, info, true, false, a.desktopSessionService(""))
	ref := session.SessionRef{HostID: localDesktopHostID, SessionID: id}
	presentation := state.Presentation[id]
	topicID := presentation.TopicID
	if topicID == "" {
		topicID = "canonical-" + id
	}
	kind := "topic"
	if workspace.ID == workspacestate.GlobalWorkspaceID {
		kind = "global_topic"
	}
	createdAt := row.CreatedAt
	if previous := createdTopics[topicID]; previous > 0 {
		createdAt = previous
	}
	node := ProjectNode{
		Key: "canonical_" + id, Kind: kind, Label: a.localizedTopicTitle(sessionDisplayTitle(info, presentation)), Root: workspace.Root,
		TopicID: topicID, Session: &ref, SessionPath: sessionRoute(id), CanArchive: row.Health != "missing",
		Preview: row.Preview, Turns: row.Turns, TurnsState: row.MetadataStatus, Health: row.Health,
		CreatedAt: createdAt, LastActivityAt: row.UpdatedAt, ResultSequence: row.ResultSequence, Open: row.Running,
		Pinned: presentation.Pinned, SortOrder: index, Children: []ProjectNode{}, SessionOrigin: row.Origin,
		LifecycleGeneration: state.SessionStates[id].Generation,
	}
	if row.ParentSessionID != "" {
		node.ParentSession = &session.SessionRef{HostID: localDesktopHostID, SessionID: row.ParentSessionID}
	}
	return node
}

func filterWorkspaceSessionNodes(req ProjectTopicPageRequest, org workspacestate.Organization, state workspacestate.State, workspaceID string, nodes []ProjectNode) []ProjectNode {
	ranks := map[string]int{}
	for i, key := range org.Order {
		ranks[key] = i
	}
	filtered := []ProjectNode{}
	seen := map[string]bool{}
	cutoff := projectTopicTimeCutoff(req)
	query := strings.ToLower(strings.TrimSpace(req.Query))
	aliases := workspaceSourceAliases(state, workspaceID)
	for _, n := range nodes {
		if req.pinnedOnly && !n.Pinned {
			continue
		}
		key := projectNodeSessionKey(n)
		if seen[key] {
			continue
		}
		seen[key] = true
		n.SortOrder = -1
		if org.ManualOrderEnabled {
			if rank, ok := ranks[key]; ok {
				n.SortOrder = rank
			}
		}
		if n.Session != nil {
			n.IdentityAliases = aliases[n.Session.SessionID]
			n.LifecycleGeneration = state.SessionStates[n.Session.SessionID].Generation
		}
		if !projectNodeRequestAllows(req, n) || cutoff > 0 && max(n.CreatedAt, n.LastActivityAt) < cutoff {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(n.Label+"\n"+n.Preview+"\n"+key), query) {
			continue
		}
		filtered = append(filtered, n)
	}
	return filtered
}
