package main

import (
	"context"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/sessioncatalog"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Sidebar reads are bound so a starved connection pool or a slow projection
// degrades into a stale page the next revision repairs, instead of a Wails call
// that never returns and a tree that never moves again.
const sessionCatalogReadTimeout = 10 * time.Second

func (a *App) catalogReadContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.bootContext(), sessionCatalogReadTimeout)
}

type catalogRuntimeSnapshot struct {
	tabID            string
	scope            string
	workspaceRoot    string
	topicID          string
	sessionPath      string
	sessionHeadID    string
	activity         string
	topicTitle       string
	topicTitleSource string
	ctrl             control.SessionAPI
	state            *event.RuntimeStateSnapshot
	open             bool
}

type catalogRuntimeOverlay struct {
	open    bool
	running bool
	status  string
}

func (a *App) catalogRuntimeOverlays() (map[string]catalogRuntimeOverlay, map[string]catalogRuntimeOverlay) {
	topics := map[string]catalogRuntimeOverlay{}
	sessions := map[string]catalogRuntimeOverlay{}
	for _, snap := range a.catalogRuntimeSnapshots() {
		path := strings.TrimSpace(snap.sessionPath)
		if snap.ctrl != nil {
			if path == "" {
				path = snap.ctrl.SessionPath()
			}
		}
		status, running := catalogControllerStatus(snap.ctrl, snap.activity)
		overlay := catalogRuntimeOverlay{open: snap.open, running: running, status: status}
		key := topicSummaryKey(snap.scope, snap.workspaceRoot, snap.topicID)
		current := topics[key]
		current.open = current.open || overlay.open
		current.running = current.running || overlay.running
		if current.status == "" {
			current.status = overlay.status
		}
		topics[key] = current
		if path != "" {
			sessions[sessionRuntimeKey(path)] = overlay
		}
	}
	return topics, sessions
}

func (a *App) metadataProjectTopics(scope, workspaceRoot string) []ProjectNode {
	f := loadProjectsFile()
	deleted := map[string]bool{}
	for _, topicID := range f.DeletedTopics {
		deleted[topicID] = true
	}
	if state, err := a.workspaceRegistry().Load(a.bootContext()); err == nil {
		for topicID := range purgedCanonicalTopicIDs(state) {
			deleted[topicID] = true
		}
	}
	ids := f.GlobalTopics
	pinnedIDs := f.GlobalPinnedTopics
	titleRoot := ""
	projectColor := normalizeProjectColor(f.GlobalColor)
	if scope == "project" {
		ids = nil
		pinnedIDs = nil
		titleRoot = workspaceRoot
		for _, project := range f.Projects {
			if sameProjectRoot(project.Root, workspaceRoot) {
				ids = project.Topics
				pinnedIDs = project.PinnedTopics
				projectColor = project.Color
				break
			}
		}
	}
	titles := loadTopicTitles(titleRoot)
	sources := loadTopicTitleSources(titleRoot)
	created := loadTopicCreatedAts(titleRoot)
	topicOverlays, _ := a.catalogRuntimeOverlays()
	runtimeNodes := a.runtimeOnlyProjectTopics(scope, workspaceRoot)
	runtimeByTopic := map[string][]ProjectNode{}
	for _, node := range runtimeNodes {
		runtimeByTopic[node.TopicID] = append(runtimeByTopic[node.TopicID], node)
	}
	out := []ProjectNode{}
	seen := map[string]bool{}
	for _, topicID := range pinnedTopicIDs(orderedTopicIDs(ids, titles), pinnedIDs) {
		if deleted[topicID] {
			continue
		}
		seen[topicID] = true
		title := strings.TrimSpace(titles[topicID])
		if title == "" {
			title = defaultTopicTitle
		}
		kind := "topic"
		if scope != "project" {
			kind = "global_topic"
		}
		overlay := topicOverlays[topicSummaryKey(scope, workspaceRoot, topicID)]
		node := ProjectNode{
			Key: kind + "_" + topicID, Kind: kind,
			Label: a.localizedTopicTitle(title, sources[topicID]), Root: workspaceRoot,
			TopicID: topicID, ProjectColor: projectColor,
			CreatedAt: topicCreatedAtForTree(created, topicID), Pinned: containsDesktopString(pinnedIDs, topicID), SortOrder: -1,
			Open: overlay.open, Running: overlay.running, Status: overlay.status,
			TurnsState: string(sessioncatalog.TurnsUnknown), Health: string(sessioncatalog.HealthOK),
			Children: []ProjectNode{},
		}
		if runtimeRows := runtimeByTopic[topicID]; len(runtimeRows) > 0 {
			for _, runtimeNode := range runtimeRows {
				runtimeNode.Pinned, runtimeNode.SortOrder = node.Pinned, node.SortOrder
				runtimeNode.CreatedAt, runtimeNode.ProjectColor = node.CreatedAt, node.ProjectColor
				if strings.TrimSpace(runtimeNode.Label) == "" {
					runtimeNode.Label = node.Label
				}
				out = append(out, runtimeNode)
			}
			continue
		}
		out = append(out, node)
	}
	for _, runtimeNode := range runtimeNodes {
		if seen[runtimeNode.TopicID] || deleted[runtimeNode.TopicID] {
			continue
		}
		runtimeNode.RuntimeOnly = true
		out = append(out, runtimeNode)
	}
	return out
}

func (a *App) runtimeOnlyProjectTopics(scope, workspaceRoot string) []ProjectNode {
	nodes, _ := a.runtimeOnlyProjectTopicsWithSessions(scope, workspaceRoot)
	return nodes
}

// runtimeOnlyProjectTopicsWithSessions also reports each runtime topic's known
// session paths so callers can resolve the topics those sessions project onto
// in the catalog (a restored tab may carry a legacy topic ID for a re-anchored
// recovery lineage).
func (a *App) runtimeOnlyProjectTopicsWithSessions(scope, workspaceRoot string) ([]ProjectNode, map[string][]string) {
	snapshots := []catalogRuntimeSnapshot{}
	for _, snapshot := range a.catalogRuntimeSnapshots() {
		if scope == "project" {
			if snapshot.scope != "project" || !sameProjectRoot(snapshot.workspaceRoot, workspaceRoot) {
				continue
			}
		} else if snapshot.scope == "project" {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	return a.runtimeProjectTopicNodes(scope, workspaceRoot, snapshots, true)
}

func (a *App) runtimeProjectTopicNodes(scope, workspaceRoot string, snapshots []catalogRuntimeSnapshot, previews bool) ([]ProjectNode, map[string][]string) {
	sessionsByTopic := map[string][]string{}
	out := []ProjectNode{}
	kind := "topic"
	if scope != "project" {
		kind = "global_topic"
	}
	for _, snapshot := range snapshots {
		if snapshot.sessionPath == "" && snapshot.ctrl != nil {
			snapshot.sessionPath = snapshot.ctrl.SessionPath()
		}
		path := strings.TrimSpace(snapshot.sessionPath)
		if path != "" {
			sessionsByTopic[snapshot.topicID] = append(sessionsByTopic[snapshot.topicID], path)
		}
		label := defaultTopicTitle
		if strings.TrimSpace(snapshot.topicTitle) != "" {
			label = snapshot.topicTitle
		}
		// A canonical route is an internal identity, never a display name. Only
		// legacy file-backed sessions may use their filename as the last-resort
		// runtime label while the catalog is catching up.
		if _, canonical := parseSessionRoute(path); !canonical {
			if pathLabel := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)); pathLabel != "" && pathLabel != "." {
				label = pathLabel
			}
		}
		var status string
		var running bool
		if snapshot.state != nil {
			status, running = catalogStateStatus(*snapshot.state, snapshot.activity)
		} else {
			status, running = catalogControllerStatus(snapshot.ctrl, snapshot.activity)
		}
		preview := ""
		if previews {
			preview = sessionPreviewForPath(path)
		}
		key := kind + "_" + snapshot.topicID
		if path != "" {
			key = projectSessionNodeKey(scope, path)
		}
		out = append(out, ProjectNode{
			Key: key, Kind: kind, Label: a.localizedTopicTitle(label, snapshot.topicTitleSource),
			Root: workspaceRoot, TopicID: snapshot.topicID, SessionPath: path, Preview: preview,
			Open: snapshot.open, Running: running, Status: status,
			TurnsState: string(sessioncatalog.TurnsUnknown), Health: string(sessioncatalog.HealthOK),
			Children: []ProjectNode{},
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, sessionsByTopic
}

func normalizeDesktopTopicScope(scope, workspaceRoot string) (string, string) {
	if strings.TrimSpace(scope) != "project" {
		return "global", ""
	}
	return "project", strings.TrimSpace(workspaceRoot)
}

func projectTopicSortValue(createdAt, lastActivityAt int64, sortMode string) int64 {
	if strings.TrimSpace(sortMode) == "created" {
		if createdAt > 0 {
			return createdAt
		}
		return lastActivityAt
	}
	if lastActivityAt > 0 {
		return lastActivityAt
	}
	return createdAt
}

func (a *App) GetSessionCatalogStatus() SessionCatalogStatus {
	return a.currentSessionCatalogStatus()
}
