package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxSessionGroups       = 100
	maxSessionGroupIDRunes = 160
	maxSessionGroupRunes   = 120
	maxSessionGroupTopics  = 10_000
)

type desktopProject struct {
	Root               string         `json:"root"`
	Title              string         `json:"title,omitempty"`
	Color              string         `json:"color,omitempty"`
	Topics             []string       `json:"topics"`
	PinnedTopics       []string       `json:"pinnedTopics,omitempty"`
	ManualTopicOrder   bool           `json:"-"`
	SessionOrder       []string       `json:"-"`
	ManualSessionOrder bool           `json:"-"`
	Groups             []desktopGroup `json:"-"`
	GroupsRevision     uint64         `json:"-"`
}

type desktopGroup struct {
	ID                  string   `json:"id"`
	Title               string   `json:"title"`
	TopicIDs            []string `json:"topicIds,omitempty"`
	SessionKeys         []string `json:"sessionKeys,omitempty"`
	ExcludedSessionKeys []string `json:"excludedSessionKeys,omitempty"`
}

type desktopProjectFile struct {
	GlobalTitle              string           `json:"globalTitle,omitempty"`
	GlobalColor              string           `json:"globalColor,omitempty"`
	GlobalTopics             []string         `json:"globalTopics,omitempty"`
	GlobalPinnedTopics       []string         `json:"globalPinnedTopics,omitempty"`
	GlobalManualTopicOrder   bool             `json:"-"`
	GlobalSessionOrder       []string         `json:"-"`
	GlobalManualSessionOrder bool             `json:"-"`
	GlobalGroups             []desktopGroup   `json:"-"`
	GlobalGroupsRevision     uint64           `json:"-"`
	DeletedTopics            []string         `json:"deletedTopics,omitempty"`
	PinnedProjects           []string         `json:"pinnedProjects,omitempty"`
	SidebarOrder             []string         `json:"sidebarOrder,omitempty"`
	Projects                 []desktopProject `json:"projects"`
}

func normalizeGroups(groups []desktopGroup) []desktopGroup {
	out := make([]desktopGroup, 0, len(groups))
	seenGroups := make(map[string]bool, len(groups))
	seenTopics := make(map[string]bool)
	seenSessions := make(map[string]bool)
	for _, group := range groups {
		group.ID = strings.TrimSpace(group.ID)
		group.Title = strings.TrimSpace(group.Title)
		if group.ID == "" || seenGroups[group.ID] {
			continue
		}
		seenGroups[group.ID] = true
		topics := make([]string, 0, len(group.TopicIDs))
		for _, topicID := range group.TopicIDs {
			topicID = strings.TrimSpace(topicID)
			if topicID == "" || seenTopics[topicID] {
				continue
			}
			seenTopics[topicID] = true
			topics = append(topics, topicID)
		}
		group.TopicIDs = topics
		var sessions []string
		if len(group.SessionKeys) > 0 {
			sessions = make([]string, 0, len(group.SessionKeys))
		}
		for _, key := range group.SessionKeys {
			key = strings.TrimSpace(key)
			if key == "" || seenSessions[key] {
				continue
			}
			seenSessions[key] = true
			sessions = append(sessions, key)
		}
		group.SessionKeys = sessions
		var excluded []string
		if len(group.ExcludedSessionKeys) > 0 {
			excluded = make([]string, 0, len(group.ExcludedSessionKeys))
		}
		seenExcluded := map[string]bool{}
		for _, key := range group.ExcludedSessionKeys {
			key = strings.TrimSpace(key)
			if key == "" || seenExcluded[key] {
				continue
			}
			seenExcluded[key] = true
			excluded = append(excluded, key)
		}
		group.ExcludedSessionKeys = excluded
		out = append(out, group)
	}
	return out
}

func mergeDesktopGroups(left, right []desktopGroup) []desktopGroup {
	merged := append(append([]desktopGroup(nil), left...), right...)
	byID := make(map[string]int, len(merged))
	out := make([]desktopGroup, 0, len(merged))
	for _, group := range merged {
		group.ID = strings.TrimSpace(group.ID)
		if group.ID == "" {
			continue
		}
		if index, ok := byID[group.ID]; ok {
			if out[index].Title == "" {
				out[index].Title = strings.TrimSpace(group.Title)
			}
			out[index].TopicIDs = append(out[index].TopicIDs, group.TopicIDs...)
			out[index].SessionKeys = append(out[index].SessionKeys, group.SessionKeys...)
			out[index].ExcludedSessionKeys = append(out[index].ExcludedSessionKeys, group.ExcludedSessionKeys...)
			continue
		}
		byID[group.ID] = len(out)
		out = append(out, group)
	}
	return normalizeGroups(out)
}

func validateSessionGroups(groups []desktopGroup) error {
	if len(groups) > maxSessionGroups {
		return fmt.Errorf("too many session groups: %d", len(groups))
	}
	seenGroups := make(map[string]bool, len(groups))
	seenTopics := make(map[string]bool)
	seenSessions := make(map[string]bool)
	for _, group := range groups {
		id, title := strings.TrimSpace(group.ID), strings.TrimSpace(group.Title)
		if id == "" || title == "" {
			return fmt.Errorf("session group id and title are required")
		}
		if utf8.RuneCountInString(id) > maxSessionGroupIDRunes || utf8.RuneCountInString(title) > maxSessionGroupRunes {
			return fmt.Errorf("session group id or title is too long")
		}
		if seenGroups[id] {
			return fmt.Errorf("duplicate session group %q", id)
		}
		seenGroups[id] = true
		if len(group.TopicIDs)+len(group.SessionKeys)+len(group.ExcludedSessionKeys) > maxSessionGroupTopics {
			return fmt.Errorf("session group %q has too many topics", id)
		}
		for _, topicID := range group.TopicIDs {
			topicID = strings.TrimSpace(topicID)
			if topicID == "" || seenTopics[topicID] {
				return fmt.Errorf("invalid or duplicate grouped topic %q", topicID)
			}
			seenTopics[topicID] = true
		}
		for _, key := range group.SessionKeys {
			key = strings.TrimSpace(key)
			if key == "" || seenSessions[key] {
				return fmt.Errorf("invalid or duplicate grouped session %q", key)
			}
			seenSessions[key] = true
		}
		seenExcluded := map[string]bool{}
		for _, key := range group.ExcludedSessionKeys {
			key = strings.TrimSpace(key)
			if key == "" || seenExcluded[key] {
				return fmt.Errorf("invalid or duplicate excluded session %q", key)
			}
			seenExcluded[key] = true
		}
	}
	return nil
}

func normalizeOrganizationTarget(scope, workspaceRoot string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "global" {
		return scope, "", nil
	}
	if scope != "project" {
		return "", "", fmt.Errorf("unsupported scope %q", scope)
	}
	workspaceRoot = normalizeProjectRoot(workspaceRoot)
	if workspaceRoot == "" {
		return "", "", fmt.Errorf("workspaceRoot is required for project scope")
	}
	return scope, workspaceRoot, nil
}

func groupsWithoutTopic(groups []desktopGroup, topicID string) ([]desktopGroup, bool) {
	next := make([]desktopGroup, len(groups))
	changed := false
	for i, group := range groups {
		next[i] = group
		next[i].TopicIDs = removeString(group.TopicIDs, topicID)
		changed = changed || !sameStringList(next[i].TopicIDs, group.TopicIDs)
	}
	return next, changed
}
