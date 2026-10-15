package main

import (
	"errors"
	"os"
	"strings"

	"deepzora/internal/agent"
)

var (
	errTopicHasActiveWork = errors.New("wait for the session to finish, answer pending prompts, and stop background jobs before archiving this topic")
	errTopicArchiveBusy   = errors.New("Deepzora is finishing another session change — wait a moment and retry archiving")
)

func (a *App) TrashTopic(topicID string) error {
	return friendlySessionFileError(a.archiveCompatibleTopic(topicID))
}

func (a *App) topicHasActiveRuntimeWork(topicID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tabs := range []map[string]*WorkspaceTab{a.tabs, a.detachedSessions} {
		for _, tab := range tabs {
			if tab != nil && tab.TopicID == topicID && tab.hasActiveRuntimeWork() {
				return true
			}
		}
	}
	return false
}

func topicIndexedInRegistry(scope, workspaceRoot, topicID string) bool {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return false
	}
	if strings.TrimSpace(loadTopicTitles(topicTitleRoot(scope, workspaceRoot))[topicID]) != "" {
		return true
	}
	f := loadProjectsFile()
	if scope != "project" {
		return containsDesktopString(f.GlobalTopics, topicID)
	}
	if i := projectIndexByRoot(f.Projects, workspaceRoot); i >= 0 {
		return containsDesktopString(f.Projects[i].Topics, topicID)
	}
	return false
}

func unusedTransientBlankSession(dir, path string) bool {
	resolved, ok := pinnedTabSessionPath(dir, path)
	if !ok {
		return false
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() || info.Size() != 0 {
		return false
	}
	meta, ok, err := agent.LoadBranchMeta(resolved)
	if err != nil || !ok {
		return true
	}
	topicID := strings.TrimSpace(meta.TopicID)
	if topicID == "" {
		return true
	}
	if !isDefaultTopicTitle(meta.TopicTitle) && strings.TrimSpace(meta.TopicTitle) != "" {
		return false
	}
	// A zero-byte default sidecar stays transient after registry projection.
	// The caller's keep set protects every visible or detached runtime; registry
	// presence alone cannot prove user content.
	return true
}
