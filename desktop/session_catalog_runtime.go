package main

import (
	"deepzora/internal/control"
	"deepzora/internal/event"
	"sort"
	"strings"
)

type catalogRuntimeSnapshot struct {
	tabID            string
	scope            string
	workspaceRoot    string
	topicID          string
	sessionPath      string
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

func (a *App) catalogSessionRuntimeOverlays() map[string]catalogRuntimeOverlay {
	sessions := map[string]catalogRuntimeOverlay{}
	for _, snap := range a.catalogRuntimeSnapshots() {
		path := strings.TrimSpace(snap.sessionPath)
		if snap.ctrl != nil {
			if path == "" {
				path = snap.ctrl.SessionRoute()
			}
		}
		status, running := catalogControllerStatus(snap.ctrl, snap.activity)
		overlay := catalogRuntimeOverlay{open: snap.open, running: running, status: status}
		if path != "" {
			sessions[sessionRuntimeKey(path)] = overlay
		}
	}
	return sessions
}

func (a *App) runtimeProjectTopicNodes(scope, workspaceRoot string, snapshots []catalogRuntimeSnapshot) []ProjectNode {
	out := []ProjectNode{}
	kind := "topic"
	if scope != "project" {
		kind = "global_topic"
	}
	for _, snapshot := range snapshots {
		if snapshot.sessionPath == "" && snapshot.ctrl != nil {
			snapshot.sessionPath = snapshot.ctrl.SessionRoute()
		}
		path := strings.TrimSpace(snapshot.sessionPath)
		label := defaultTopicTitle
		if strings.TrimSpace(snapshot.topicTitle) != "" {
			label = snapshot.topicTitle
		}
		var status string
		var running bool
		if snapshot.state != nil {
			status, running = catalogStateStatus(*snapshot.state, snapshot.activity)
		} else {
			status, running = catalogControllerStatus(snapshot.ctrl, snapshot.activity)
		}
		key := kind + "_" + snapshot.topicID
		if path != "" {
			key = projectSessionNodeKey(scope, path)
		}
		out = append(out, ProjectNode{
			Key: key, Kind: kind, Label: a.localizedTopicTitle(label, snapshot.topicTitleSource),
			Root: workspaceRoot, TopicID: snapshot.topicID, SessionPath: path,
			Open: snapshot.open, Running: running, Status: status,
			TurnsState: "unknown", Health: "healthy",
			Children: []ProjectNode{},
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
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
