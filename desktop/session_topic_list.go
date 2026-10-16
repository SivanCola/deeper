package main

import "strings"

// ListProjectTopics reads current registered sessions; remote roots never map to local storage.
func (a *App) ListProjectTopics(req ProjectTopicPageRequest) (ProjectTopicPage, error) {
	// Remote roots come from Serve and must never map to local metadata paths.
	if strings.HasPrefix(strings.TrimSpace(req.WorkspaceRoot), "remote-project:") {
		return ProjectTopicPage{Items: []ProjectNode{}}, nil
	}
	return a.unifiedProjectTopics(req)
}
