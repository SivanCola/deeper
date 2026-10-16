package main

import (
	"context"
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/history"
	"deepzora/internal/stats"
	"deepzora/internal/taskcatalog"
	"strings"
	"time"
)

type SessionCatalogStatus struct {
	State         string `json:"state"`
	Revision      uint64 `json:"revision"`
	Indexed       int64  `json:"indexed"`
	Total         int64  `json:"total"`
	RepairPending int64  `json:"repairPending"`
	RepairActive  int64  `json:"repairActive"`
	RepairBlocked int64  `json:"repairBlocked"`
	CanRebuild    bool   `json:"canRebuild"`
	LastError     string `json:"lastError,omitempty"`
}

type ProjectTreeSnapshot struct {
	Revision            uint64               `json:"revision"`
	WorkspaceGeneration *uint64              `json:"workspaceGeneration,omitempty"`
	Projects            []ProjectNode        `json:"projects"`
	Catalog             SessionCatalogStatus `json:"catalog"`
	Indexed             int64                `json:"indexed"`
	Total               int64                `json:"total"`
	IndexingDone        bool                 `json:"indexingDone"`
}

type ProjectTopicPageRequest struct {
	Scope         string `json:"scope"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Query         string `json:"query,omitempty"`
	TimeFilter    string `json:"timeFilter,omitempty"`
	SortMode      string `json:"sortMode,omitempty"`
	GroupFilter   string `json:"groupFilter,omitempty"`
	GroupID       string `json:"groupId,omitempty"`
	ExcludePinned bool   `json:"excludePinned,omitempty"`

	groupSelected *desktopGroup
	groupAll      []desktopGroup
	pinnedOnly    bool
	timeCutoff    int64
}

type ProjectTopicKey struct {
	Scope         string `json:"scope"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	TopicID       string `json:"topicId"`
	// Path optionally binds topic-wide recovery actions to one physical lineage.
	// Older frontends omit it and remain compatible when the topic has one group.
	Path string `json:"path,omitempty"`
	// RecordClassification is set only by the recovery-event coordinator after
	// a catalog revision. Ordinary History reads remain diagnostic-free.
	RecordClassification bool `json:"recordClassification,omitempty"`
}

type ProjectTopicPage struct {
	SnapshotID         string        `json:"snapshotId,omitempty"`
	SnapshotExpiresAt  int64         `json:"snapshotExpiresAt,omitempty"`
	Items              []ProjectNode `json:"items"`
	NextCursor         string        `json:"nextCursor,omitempty"`
	Revision           uint64        `json:"revision"`
	Complete           bool          `json:"complete"`
	ReadyDirectories   int           `json:"readyDirectories"`
	PendingDirectories int           `json:"pendingDirectories"`
	FailedDirectories  int           `json:"failedDirectories"`
}

type ProjectTreeChangedV2 struct {
	Revision uint64   `json:"revision"`
	Roots    []string `json:"roots"`
	Reason   string   `json:"reason"`
}

// ProjectRuntimeTopic is one process-local runtime projected onto its stable
// logical topic identity. The catalog remains the authority for persisted
// history; this projection is the authority for what this process is running.
type ProjectRuntimeTopic struct {
	Scope         string      `json:"scope"`
	WorkspaceRoot string      `json:"workspaceRoot,omitempty"`
	Node          ProjectNode `json:"node"`
}

// ProjectTreeRuntimeSnapshot is a replace-all, idempotent runtime projection.
// Its revision is independent from the session catalog revision so clients can
// order ownership/status changes without reloading any catalog page.
type ProjectTreeRuntimeSnapshot struct {
	Revision uint64                `json:"revision"`
	Topics   []ProjectRuntimeTopic `json:"topics"`
}

func flushDesktopDerivedCatalogs(ctx context.Context) error {
	var first error
	if err := history.FlushSharedCatalog(ctx); err != nil && first == nil {
		first = err
	}
	if err := history.CloseSharedCatalog(ctx); err != nil && first == nil {
		first = err
	}
	if err := stats.CloseUsageCatalogs(ctx); err != nil && first == nil {
		first = err
	}
	if err := taskcatalog.ShutdownShared(ctx); err != nil && first == nil {
		first = err
	}
	return first
}

func (a *App) currentSessionCatalogStatus() SessionCatalogStatus {
	if a == nil {
		return SessionCatalogStatus{State: "closed"}
	}
	state, err := a.workspaceRegistry().LoadProjection(a.bootContext())
	if err != nil {
		return SessionCatalogStatus{State: "degraded", LastError: err.Error()}
	}
	return a.currentSessionCatalogStatusFor(state)
}

func (a *App) currentSessionCatalogStatusFor(state workspacestate.State) SessionCatalogStatus {
	service := a.desktopSessionService("")
	if service == nil {
		return SessionCatalogStatus{State: "degraded", LastError: "Current session service is unavailable"}
	}
	metadata := service.Query().CatalogState()
	status := SessionCatalogStatus{State: "ready", Revision: metadata.Revision,
		RepairPending: metadata.Pending, RepairActive: metadata.Active, RepairBlocked: metadata.Failed,
		LastError: metadata.LastError, CanRebuild: true}
	for _, row := range state.SessionStates {
		if row.Lifecycle != workspacestate.Deleted {
			status.Total++
		}
	}
	status.Indexed = max(0, status.Total-metadata.Pending-metadata.Failed)
	if metadata.Pending > 0 {
		status.State = "opening"
	}
	if metadata.Failed > 0 {
		status.State = "degraded"
	}
	if a.catalogRebuilding.Load() {
		status.State, status.CanRebuild = "rebuilding", false
	}
	if a.shuttingDown.Load() {
		status.State, status.CanRebuild = "closed", false
	}
	return status
}

func (a *App) startSessionCatalog() {
	if a == nil || a.shuttingDown.Load() {
		return
	}
	a.catalogLifecycleMu.Lock()
	if a.catalogCancel != nil {
		a.catalogLifecycleMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(a.bootContext())
	done := make(chan struct{})
	initialReconcileDone := make(chan struct{})
	metadataRequests := make(chan struct{}, 1)
	a.catalogCancel = cancel
	a.catalogContext = ctx
	a.catalogDone = done
	a.catalogInitialReconcileDone = initialReconcileDone
	a.catalogMetadataRequests = metadataRequests
	a.catalogLifecycleMu.Unlock()

	go func() {
		defer close(done)
		a.runSessionCatalog(ctx, initialReconcileDone, metadataRequests)
	}()
}

// sessionCatalogClose is one stop's release of the catalog handles it took.
type sessionCatalogClose struct {
	done chan struct{}
}

// stopSessionCatalog reports whether every catalog handle this App opened is
// released. A close that outlives its deadline keeps running and stays owed:
// the next stop waits for it rather than reporting a handle it has forgotten.
func (a *App) stopSessionCatalog(timeout time.Duration) bool {
	if a == nil {
		return true
	}
	deadline := time.Now().Add(timeout)
	closing := &sessionCatalogClose{done: make(chan struct{})}
	a.catalogLifecycleMu.Lock()
	cancel, done, earlier := a.catalogCancel, a.catalogDone, a.catalogClosing
	a.catalogCancel, a.catalogContext, a.catalogDone, a.catalogInitialReconcileDone, a.catalogMetadataRequests = nil, nil, nil, nil, nil
	a.catalogClosing = closing
	a.catalogLifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.catalogRebuildMu.Lock()
	flight := a.catalogRebuild
	a.catalogRebuildMu.Unlock()
	go func() {
		defer close(closing.done)
		if done != nil {
			<-done
		}
		if flight != nil {
			<-flight.done
		}
		if earlier != nil {
			<-earlier.done
		}
	}()
	return waitChannelBefore(closing.done, deadline)
}

func waitChannelBefore(done <-chan struct{}, deadline time.Time) bool {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return false
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (a *App) cancelAllTabBuilds() {
	if a == nil {
		return
	}
	a.mu.Lock()
	for _, tab := range a.tabs {
		a.supersedeTabBuildLocked(tab)
	}
	for _, tab := range a.detachedSessions {
		a.supersedeTabBuildLocked(tab)
	}
	a.mu.Unlock()
}

func (a *App) emitProjectTreeChangedV2(revision uint64, roots []string, reason string) {
	if roots == nil {
		roots = []string{}
	}
	a.emitRuntimeEvent("project-tree:changed-v2", ProjectTreeChangedV2{Revision: a.unifiedProjectRevision(revision), Roots: roots, Reason: reason})
}

func (a *App) requestSessionCatalogMetadataSync() {
	if a.shuttingDown.Load() {
		return
	}
	a.catalogLifecycleMu.Lock()
	requests := a.catalogMetadataRequests
	a.catalogLifecycleMu.Unlock()
	// Every source shares the watcher's worker, including user edits. A nil
	// channel before startup/after shutdown simply has no receiver to wake.
	select {
	case requests <- struct{}{}:
	default:
	}
}

func (a *App) GetProjectTreeSnapshot() (ProjectTreeSnapshot, error) {
	// Membership/visibility and its generation come from one verified registry
	// snapshot. Never silently replace it with legacy membership on read failure.
	state, versions, err := a.workspaceRegistry().LoadProjectionWithVersions(a.bootContext())
	if err != nil {
		return ProjectTreeSnapshot{Projects: []ProjectNode{}}, err
	}
	f := loadProjectsFile()
	projects := []ProjectNode{}
	hasProject := len(f.Projects) > 0
	for id, workspace := range state.Workspaces {
		if id != workspacestate.GlobalWorkspaceID && workspace.Visible {
			hasProject = true
		}
	}
	if strings.TrimSpace(f.GlobalTitle) != "" || !hasProject {
		label := strings.TrimSpace(f.GlobalTitle)
		if label == "" {
			label = "Global"
		}
		projects = append(projects, ProjectNode{
			Key: "global_folder", Kind: "global_folder", Label: label,
			Root: globalWorkspaceRoot(), ProjectColor: normalizeProjectColor(f.GlobalColor),
			Children: []ProjectNode{},
		})
	}
	for _, project := range f.Projects {
		label := strings.TrimSpace(project.Title)
		if label == "" {
			label = workspaceName(project.Root)
		}
		projects = append(projects, ProjectNode{
			Key: "project_" + project.Root, Kind: "project", Label: label,
			Root: project.Root, ProjectColor: project.Color,
			Pinned:   containsDesktopString(f.PinnedProjects, project.Root),
			Children: []ProjectNode{},
		})
	}
	// Remote projects (pinned via the connection wizard) render as project
	// groups too; the Remote ref swaps the folder icon for a cloud icon.
	if remoteNodes, err := a.remoteProjectNodes(); err == nil {
		projects = append(projects, remoteNodes...)
	}
	projects = a.mergeCanonicalWorkspaceShellsFromProjection(projects, state, versions)
	projects = applyPinnedProjectOrder(applyProjectTreeOrder(projects, f.SidebarOrder), f.PinnedProjects)
	status := a.currentSessionCatalogStatusFor(state)
	return ProjectTreeSnapshot{
		Revision: status.Revision + state.Generation, WorkspaceGeneration: &state.Generation, Projects: projects, Catalog: status,
		Indexed: status.Indexed, Total: status.Total,
		IndexingDone: a.catalogIndexingDone(status),
	}, nil
}

func (a *App) catalogIndexingDone(status SessionCatalogStatus) bool {
	return status.State == "ready" && status.RepairPending == 0 && status.RepairBlocked == 0
}
