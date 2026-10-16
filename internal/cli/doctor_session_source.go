package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"deepzora/internal/config"
	"deepzora/internal/session"
)

type doctorSessionSource struct {
	service       *session.Service
	ref           session.SessionRef
	telemetryPath string
}

// Diagnostics resolve an exact identity without opening a writer, replaying
// old files, or choosing a conversation from a preview-text match.
func openDoctorSession(raw string) (doctorSessionSource, error) {
	id := strings.TrimPrefix(strings.TrimSpace(raw), "session-id:")
	if err := session.ValidateSessionID(id); err != nil {
		return doctorSessionSource{}, fmt.Errorf("current session identity required: %w", err)
	}
	var targets []sessionCatalogTarget
	for _, root := range currentSessionCatalogRoots(nil) {
		targets = append(targets, sessionCatalogTarget{Path: root})
	}
	if cwd, err := os.Getwd(); err == nil {
		targets = append(targets, sessionCatalogTarget{Path: session.RootForSessionDir(config.ProjectSessionDir(cwd))})
	}
	ctx := context.Background()
	var found doctorSessionSource
	var resultErr error
	defer func() {
		if resultErr != nil && found.service != nil {
			_ = found.service.Shutdown(ctx)
		}
	}()
	for _, target := range uniqueSessionCatalogTargets(targets) {
		service, err := session.NewService("local", session.NewFilesystemPersistence(target.Path))
		if err != nil {
			resultErr = err
			return doctorSessionSource{}, resultErr
		}
		service.MetadataOnlyListings()
		ref, err := service.Query().ResolveSessionID(ctx, id)
		if err != nil {
			_ = service.Shutdown(ctx)
			if errors.Is(err, session.ErrSessionNotFound) {
				continue
			}
			resultErr = err
			return doctorSessionSource{}, resultErr
		}
		if found.service != nil {
			_ = service.Shutdown(ctx)
			resultErr = fmt.Errorf("session identity %q exists in multiple current stores", id)
			return doctorSessionSource{}, resultErr
		}
		found = doctorSessionSource{service: service, ref: ref}
		if filepath.Clean(target.Path) == filepath.Clean(config.DesktopSessionStoreDir()) {
			found.telemetryPath = filepath.Join(config.MemoryUserDir(), "session-telemetry", ref.SessionID+".telemetry.json")
		}
	}
	if found.service == nil {
		return doctorSessionSource{}, fmt.Errorf("%w: %s", session.ErrSessionNotFound, id)
	}
	return found, nil
}
