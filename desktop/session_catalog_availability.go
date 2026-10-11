package main

import (
	"context"
	"os"

	"deepzora/internal/sessioncatalog"
)

type catalogWorkspaceAvailability struct {
	usable   bool
	complete bool
	ready    int
	pending  int
	failed   int
}

func (a *App) catalogWorkspaceAvailability(catalog *sessioncatalog.Catalog, scope, workspaceRoot string, views ...context.Context) catalogWorkspaceAvailability {
	availability := catalogWorkspaceAvailability{}
	if a == nil || catalog == nil {
		return availability
	}
	ctx, cancel := a.catalogReadContext()
	defer cancel()
	if len(views) > 0 && views[0] != nil {
		ctx = views[0]
	}
	scope, workspaceRoot = normalizeDesktopTopicScope(scope, workspaceRoot)
	for _, target := range a.sessionCatalogTargets() {
		if target.Scope != scope || scope == "project" && !sameProjectRoot(target.WorkspaceRoot, workspaceRoot) {
			continue
		}
		if !catalog.MetadataOnly() {
			if _, err := os.Stat(target.Path); os.IsNotExist(err) {
				continue
			}
		}
		// Metadata discovery owns absence: an unused optional root completes
		// empty, while a disappeared root with retained history is unavailable.
		// Re-statting here used to silently omit that failure and claim complete.
		switch catalog.DirectoryStatus(ctx, target.Path).State {
		case "ready":
			availability.ready++
		case "degraded":
			availability.failed++
		default:
			availability.pending++
		}
	}
	hasRecords := catalog.HasWorkspaceRecords(ctx, scope, workspaceRoot)
	availability.usable = hasRecords || availability.ready > 0
	availability.complete = availability.pending == 0 && availability.failed == 0 && availability.ready > 0
	if availability.ready+availability.pending+availability.failed == 0 && hasRecords {
		availability.complete = true
	}
	return availability
}
