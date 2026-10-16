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
	Root  string `json:"root"`
	Title string `json:"title,omitempty"`
	Color string `json:"color,omitempty"`
}

type desktopGroup struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	SessionKeys []string `json:"sessionKeys,omitempty"`
}

type desktopProjectFile struct {
	GlobalTitle    string           `json:"globalTitle,omitempty"`
	GlobalColor    string           `json:"globalColor,omitempty"`
	PinnedProjects []string         `json:"pinnedProjects,omitempty"`
	SidebarOrder   []string         `json:"sidebarOrder,omitempty"`
	Projects       []desktopProject `json:"projects"`
}

func validateSessionGroups(groups []desktopGroup) error {
	if len(groups) > maxSessionGroups {
		return fmt.Errorf("too many session groups: %d", len(groups))
	}
	seenGroups := make(map[string]bool, len(groups))
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
		if len(group.SessionKeys) > maxSessionGroupTopics {
			return fmt.Errorf("session group %q has too many topics", id)
		}
		for _, key := range group.SessionKeys {
			key = strings.TrimSpace(key)
			if key == "" || seenSessions[key] {
				return fmt.Errorf("invalid or duplicate grouped session %q", key)
			}
			seenSessions[key] = true
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
