package main

import (
	"errors"
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
