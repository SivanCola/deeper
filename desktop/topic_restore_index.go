package main

func topicIndexedInProjectsSnapshot(file desktopProjectFile, scope, workspaceRoot, topicID string) bool {
	if scope == "global" {
		return containsDesktopString(file.GlobalTopics, topicID)
	}
	index := projectIndexByRoot(file.Projects, workspaceRoot)
	return index >= 0 && containsDesktopString(file.Projects[index].Topics, topicID)
}

func insertTopicAt(items []string, id string, order int) []string {
	items = removeString(items, id)
	if order < 0 || order > len(items) {
		order = len(items)
	}
	items = append(items, "")
	copy(items[order+1:], items[order:])
	items[order] = id
	return items
}
