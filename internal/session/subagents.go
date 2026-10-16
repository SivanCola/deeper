package session

import (
	"context"
	"deepzora/internal/agent"
	"errors"
	"os"
	"path/filepath"
)

// Subagents returns this host's child transcript catalog. Rebuilding a parent
// controller shares its existing child writers; parent Shutdown owns their
// terminal lifecycle. Child identities never appear in the parent's List.
func (s *Service) Subagents() (*Service, error) {
	if s == nil {
		return nil, errors.New("session: subagents require a parent service")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return nil, errors.New("session: service is shut down")
	}
	if s.subagents != nil {
		return s.subagents, nil
	}
	root := s.query.StorageRoot()
	if root == "" {
		return nil, errors.New("session: subagents require filesystem persistence")
	}
	child, err := NewService(s.hostID, NewFilesystemPersistence(filepath.Join(root, ".subagents")))
	if err != nil {
		return nil, err
	}
	s.subagents = child
	return child, nil
}

// Subagents exposes the same host-owned child read model to history clients.
// It allocates no writer and shares the parent's shutdown boundary.
func (q *Query) Subagents() (*Query, error) {
	if q == nil {
		return nil, errors.New("session: parent query unavailable")
	}
	children, err := q.service.Subagents()
	if err != nil {
		return nil, err
	}
	return children.Query(), nil
}

// DeleteSubagents removes current child artifacts while the caller excludes
// parent execution (Delete and the host's tombstone transaction hold that
// exclusion). Each child deletion independently refuses a live writer.
func (s *Service) DeleteSubagents(ctx context.Context, parentID string) error {
	if err := validateSessionID(parentID); err != nil {
		return err
	}
	children, err := s.Subagents()
	if err != nil {
		return err
	}
	artifacts, err := agent.ListSubagentsByParent(children.Query().StorageRoot(), parentID)
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		err := children.Delete(ctx, SessionRef{HostID: children.HostID(), SessionID: artifact.Ref})
		if err != nil && !errors.Is(err, ErrSessionNotFound) {
			return err
		}
		if err := os.Remove(artifact.MetaPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
