package main

import (
	"fmt"
	"strings"

	"deepzora/internal/sessioncatalog"
)

// renameCatalogOnlyTopic persists a rename for a topic known only to the
// session catalog (metadata shell, no title map entry, no open tab) through
// the same title metadata path as an indexed topic, instead of failing the
// rename (#9090). It returns the usual "not found" error when the catalog
// does not list the topic either.
func (a *App) renameCatalogOnlyTopic(topicID, title string) error {
	scope, workspaceRoot, ok := a.findCatalogTopicLocation(topicID)
	if !ok {
		return fmt.Errorf("topic %q not found", topicID)
	}
	if scope == "global" {
		workspaceRoot = ""
	}
	if err := setTopicTitle(workspaceRoot, topicID, title); err != nil {
		return err
	}
	changedDirs := a.updateTopicSessionTitles(topicID, title)
	if len(changedDirs) > 0 {
		a.emitProjectTreeChangedForSessionDirs(changedDirs...)
	} else {
		a.emitProjectTreeMetadataChanged()
	}
	return nil
}

// findCatalogTopicLocation resolves a topic that exists only in the session
// catalog to its scope and workspace root.
func (a *App) findCatalogTopicLocation(topicID string) (string, string, bool) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return "", "", false
	}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return "", "", false
	}
	ctx, cancel := a.catalogReadContext()
	defer cancel()
	if _, ok, err := catalog.GetTopic(ctx, sessioncatalog.TopicKey{Scope: "global", TopicID: topicID}); err == nil && ok {
		return "global", "", true
	}
	for _, p := range loadProjectsFile().Projects {
		root := normalizeProjectRoot(p.Root)
		if root == "" {
			continue
		}
		if _, ok, err := catalog.GetTopic(ctx, sessioncatalog.TopicKey{Scope: "project", WorkspaceRoot: root, TopicID: topicID}); err == nil && ok {
			return "project", root, true
		}
	}
	return "", "", false
}
