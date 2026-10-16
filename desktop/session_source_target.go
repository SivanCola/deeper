package main

import (
	"deepzora/internal/session"
	"errors"
	"strings"
)

// Topic navigation may create an
// empty surface, but must never choose one of several durable sessions.
func (a *App) resolveTopicOpenPath(topicID string) (string, error) {
	if strings.TrimSpace(topicID) != "" {
		target, err := a.resolveSessionTarget(SessionSelector{TopicID: topicID})
		if err == nil && target.SessionPath != "" {
			return target.SessionPath, nil
		}
		var operationError *SessionOperationError
		if err != nil && (!errors.As(err, &operationError) || operationError.Code != sessionOperationTargetNotFound) {
			return "", err
		}
	}
	return "", nil
}

func (a *App) resolveSessionMutationTarget(selector SessionSelector) (SessionTarget, error) {
	return a.resolveSessionTarget(selector)
}

// Session title projection is addressed by the same durable ID as its write.
// A topic may gain another branch while an asynchronous title is generated.
func (a *App) updateCanonicalSessionTitle(ref session.SessionRef, title, source string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab != nil && tab.SessionID == ref.SessionID {
			tab.TopicTitle, tab.topicTitleSource = title, source
		}
	}
	a.saveTabsLocked()
}
