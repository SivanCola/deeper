package main

import (
	"slices"
)

// Tombstones are global to the topic ID. A replay must not remove indexes in
// a different workspace or clear that tombstone over a newly occupied ID.
// Caller owns the cross-process projects writer lock.
func topicRemovalHasOtherOwner(file desktopProjectFile, item topicRemovalEntry) bool {
	references := func(ids, pins []string) bool {
		if slices.Contains(ids, item.TopicID) || slices.Contains(pins, item.TopicID) {
			return true
		}
		return false
	}
	if item.Topic.Scope != "global" && references(file.GlobalTopics, file.GlobalPinnedTopics) {
		return true
	}
	for _, project := range file.Projects {
		if item.Topic.Scope == "project" && sameProjectRoot(project.Root, item.Topic.WorkspaceRoot) {
			continue
		}
		if references(project.Topics, project.PinnedTopics) {
			return true
		}
	}
	return false
}
