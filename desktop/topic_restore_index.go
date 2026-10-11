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

func restoreTopicGroup(groups []desktopGroup, snapshot *topicRemovalMetadata, topicID string) ([]desktopGroup, bool) {
	if snapshot == nil || snapshot.GroupID == "" {
		return groups, false
	}
	for i := range groups {
		if groups[i].ID != snapshot.GroupID {
			continue
		}
		next := insertTopicAt(groups[i].TopicIDs, topicID, snapshot.GroupOrder)
		if sameStringList(next, groups[i].TopicIDs) {
			return groups, false
		}
		groups[i].TopicIDs = next
		return groups, true
	}
	return groups, false
}
