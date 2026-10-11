package serve

import "deepzora/internal/session"

// Identity routes are registry keys, never filesystem paths. Revalidate a
// legacy target when the recovery sweep runs, not only when it was adopted.
func (s *Server) autoReclaimTarget(key string) (string, session.SessionRef, string, error) {
	if isSessionIDRoute(key) {
		ref, dir, err := s.resolveSessionIdentity(key)
		return "", ref, dir, err
	}
	path, err := s.resolveSessionPath(key)
	return path, session.SessionRef{}, "", err
}
