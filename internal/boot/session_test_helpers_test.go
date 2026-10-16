package boot

import (
	"context"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/session"
)

// withTestSession models the production host boundary explicitly. Boot no
// longer creates a persistence service as a side effect of controller assembly.
func withTestSession(t *testing.T, opts Options) Options {
	t.Helper()
	if opts.SessionService != nil {
		return opts
	}
	sessionDir := opts.SessionDir
	if sessionDir == "" {
		sessionDir = config.SessionDir()
		opts.SessionDir = sessionDir
	}
	service, err := session.NewService("local", session.NewFilesystemPersistence(session.RootForSessionDir(sessionDir)))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	opts.SessionService = service
	return opts
}
