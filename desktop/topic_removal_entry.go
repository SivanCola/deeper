package main

import "slices"

type topicRemovalMetadata struct {
	Scope         string `json:"scope"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	TitleSource   string `json:"titleSource,omitempty"`
	CreatedAt     int64  `json:"createdAt,omitempty"`
	RowRevision   int64  `json:"rowRevision,omitempty"`
	Order         int    `json:"order"`
	Pinned        bool   `json:"pinned,omitempty"`
	GroupID       string `json:"groupId,omitempty"`
	GroupOrder    int    `json:"groupOrder,omitempty"`
}

type topicRemovalEntry struct {
	ID          string
	Kind        string
	WorkspaceID string
	TopicID     string
	Title       string
	Phase       string
	Topic       *topicRemovalMetadata
}

func topicRemovalSnapshot(scope, root, topicID, source string, createdAt, rowRevision int64, order int, pinned []string, groups []desktopGroup) *topicRemovalMetadata {
	snapshot := &topicRemovalMetadata{
		Scope: scope, WorkspaceRoot: root, TitleSource: source, CreatedAt: createdAt,
		RowRevision: rowRevision, Order: order, Pinned: slices.Contains(pinned, topicID), GroupOrder: -1,
	}
	for _, group := range groups {
		if index := slices.Index(group.TopicIDs, topicID); index >= 0 {
			snapshot.GroupID, snapshot.GroupOrder = group.ID, index
			break
		}
	}
	return snapshot
}
