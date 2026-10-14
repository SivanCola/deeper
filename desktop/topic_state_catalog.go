package main

import "strings"

// ListProjectTopics surfaces topic-state failures instead of hiding them as empty pages.
func (a *App) ListProjectTopics(req ProjectTopicPageRequest) (ProjectTopicPage, error) {
	if catalog := a.sessionCatalog.Load(); catalog != nil {
		catalog.PrioritizeWorkspace(req.Scope, req.WorkspaceRoot)
	}
	// Remote roots come from Serve and must never map to local metadata paths.
	if strings.HasPrefix(strings.TrimSpace(req.WorkspaceRoot), "remote-project:") {
		return ProjectTopicPage{Items: []ProjectNode{}}, nil
	}
	if err := topicStateReadable(topicTitleRoot(req.Scope, req.WorkspaceRoot)); err != nil {
		return ProjectTopicPage{Items: []ProjectNode{}}, err
	}
	return a.unifiedProjectTopics(req)
}
