package main

import (
	"context"
	"deepzora/internal/history"
	"deepzora/internal/taskcatalog"
	"log/slog"
)

// The host observes its current query owner. No legacy directories or metadata
// databases are discovered, migrated or watched during startup.
func (a *App) runSessionCatalog(ctx context.Context, initialized chan struct{}, requests <-chan struct{}) {
	refreshRoots := func() {
		roots, err := a.currentHistoryCatalogRoots()
		if err != nil {
			slog.Warn("desktop: current history catalogs", "err", err)
			return
		}
		history.RegisterCatalogRoots(roots)
		projects := loadProjectsFile()
		taskcatalog.RegisterSharedProject(globalWorkspaceRoot(), projects.GlobalTitle)
		for _, project := range projects.Projects {
			taskcatalog.RegisterSharedProject(project.Root, projectDisplayName(project))
		}
	}
	refreshRoots()
	close(initialized)
	service := a.desktopSessionService("")
	if service == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-requests:
			refreshRoots()
		case <-service.Query().CatalogChanges():
			if ctx.Err() == nil {
				a.emitProjectTreeChangedV2(service.Query().CatalogState().Revision, nil, "metadata")
			}
		}
	}
}
