package serve

import (
	"errors"
	"path/filepath"
	"strings"

	"deepzora/internal/agent"
	"deepzora/internal/pathidentity"
	"deepzora/internal/store"
)

// resolveSessionPath validates a client-supplied transcript against the
// configured session directory and returns its symlink-resolved access path.
func (s *Server) resolveSessionPath(raw string) (string, error) {
	dir := s.ctl().SessionDir()
	if dir == "" {
		return "", errors.New("sessions disabled")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", errors.New("invalid session dir")
	}
	dirIdentity, err := pathidentity.Resolve(absDir, pathidentity.Options{FollowLeaf: true})
	if err != nil {
		return "", errors.New("invalid session dir")
	}
	absPath, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil || !store.IsSessionTranscriptName(filepath.Base(absPath)) {
		return "", errors.New("invalid session path")
	}
	pathIdentity, err := pathidentity.Resolve(absPath, pathidentity.Options{FollowLeaf: true})
	if err != nil {
		return "", errors.New("invalid session path")
	}
	rel, err := filepath.Rel(dirIdentity.Key, pathIdentity.Key)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", errors.New("path outside session dir")
	}
	// Comparison keys can case-fold directory names on macOS/Windows. Reuse
	// only the checked suffix depth, preserving the physical component spelling.
	physicalRel := ""
	physical := pathIdentity.PhysicalPath
	for suffix := rel; suffix != "."; suffix = filepath.Dir(suffix) {
		physicalRel = filepath.Join(filepath.Base(physical), physicalRel)
		physical = filepath.Dir(physical)
	}
	if !filepath.IsLocal(physicalRel) {
		return "", errors.New("path outside session dir")
	}
	// Rebuild from the trusted root and the checked relative path. Keep the
	// returned value tied to the containment check, including Windows devices.
	realPath := filepath.Join(dirIdentity.PhysicalPath, physicalRel)
	if agent.IsCleanupPending(realPath) {
		return "", errors.New("session is pending cleanup")
	}
	return realPath, nil
}
