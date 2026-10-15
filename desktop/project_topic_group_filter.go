package main

import (
	"strings"
)

func projectNodeSessionKey(node ProjectNode) string {
	if node.Session != nil && strings.TrimSpace(node.Session.SessionID) != "" {
		hostID := strings.TrimSpace(node.Session.HostID)
		if hostID == "" {
			hostID = localDesktopHostID
		}
		return "ref\x00" + hostID + "\x00" + strings.TrimSpace(node.Session.SessionID)
	}

	if path := strings.TrimSpace(node.SessionPath); path != "" {
		return "path\x00" + path
	}
	return "topic\x00" + firstNonEmpty(strings.TrimSpace(node.TopicID), strings.TrimSpace(node.Key))
}

func desktopGroupContainsNode(group desktopGroup, node ProjectNode) bool {
	key := projectNodeSessionKey(node)
	for _, explicit := range group.SessionKeys {
		if strings.TrimSpace(explicit) == key {
			return true
		}
	}
	return false
}

func projectNodeRequestAllows(req ProjectTopicPageRequest, node ProjectNode) bool {
	if req.pinnedOnly && !node.Pinned {
		return false
	}
	if req.ExcludePinned && node.Pinned {
		return false
	}
	switch req.GroupFilter {
	case "group":
		return req.groupSelected != nil && desktopGroupContainsNode(*req.groupSelected, node)
	case "ungrouped":
		for _, group := range req.groupAll {
			if desktopGroupContainsNode(group, node) {
				return false
			}
		}
		return true
	default:
		return true
	}
}
