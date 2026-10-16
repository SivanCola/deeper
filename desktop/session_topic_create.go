package main

import (
	"context"
	"errors"
	"strings"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

// createColdSessionTopic publishes a durable empty session without starting a
// controller. The pending create owns publication if the application exits
// after writing the session but before attaching it to the workspace.
func (a *App) createColdSessionTopic(scope, root, title string) (TopicMeta, error) {
	ctx := a.bootContext()
	service := a.desktopSessionService("")
	if service == nil {
		return TopicMeta{}, errors.New("session service is unavailable")
	}
	if _, err := ensureGlobalWorkspaceRoot(); err != nil {
		return TopicMeta{}, err
	}
	workspaceID, _, err := a.ensureSessionOrganization(scope, root)
	if err != nil {
		return TopicMeta{}, err
	}
	topicID := newTopicID()
	sessionID := "desktop-" + strings.TrimPrefix(newTabID(), "tab_")
	operationID := "create-" + strings.TrimPrefix(newTabID(), "tab_")
	store := a.workspaceRegistry()
	workspaceRoot := desktopWorkspaceRoot(scope, root)
	w, err := store.BeginCreateAtRoot(ctx, workspacestate.PendingCreate{
		OperationID: operationID, WorkspaceID: workspaceID, SessionID: sessionID,
	}, workspaceRoot)
	if err != nil {
		return TopicMeta{}, err
	}
	if err := store.PrepareCreatePresentation(ctx, sessionID, workspacestate.Presentation{
		TopicID: topicID, Title: title, SortOrder: -1,
	}); err != nil {
		return TopicMeta{}, err
	}
	runtime, err := service.Create(ctx, session.CreateOptions{
		SessionID: sessionID, CWD: workspaceRoot, Origin: session.SessionOriginNew,
	})
	if err != nil {
		return TopicMeta{}, err
	}
	ref := runtime.Ref()
	defer service.Close(context.Background(), ref)
	a.lifecycleCheckpoint("cold-topic-before-title")
	if title != "" {
		if err := service.SetTitle(ctx, ref, title); err != nil {
			return TopicMeta{}, err
		}
	}
	if _, err := runtime.Session().Flush(ctx); err != nil {
		return TopicMeta{}, err
	}
	if err := service.Close(ctx, ref); err != nil {
		return TopicMeta{}, err
	}
	info, err := service.Query().Stat(ctx, ref)
	if err != nil {
		return TopicMeta{}, err
	}
	if err := store.AttachCreatedSessionAtRoot(ctx, operationID, w.ID, sessionID, workspaceRoot); err != nil {
		return TopicMeta{}, err
	}
	display, source := sessionDisplayTitle(info, workspacestate.Presentation{})
	a.emitProjectTreeChanged()
	return TopicMeta{ID: topicID, Title: a.localizedTopicTitle(display, source), CreatedAt: info.CreatedAt.UnixMilli()}, nil
}
