package session

import (
	"context"
	"fmt"
	"path/filepath"
)

// StorageGeneration identifies a current persisted incarnation without reading
// its transcript. Appends preserve it; replacement invalidates retained reads.
func (q *Query) StorageGeneration(ctx context.Context, ref SessionRef) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if q == nil {
		return "", fmt.Errorf("session: nil session query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return "", err
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return "", fmt.Errorf("session: persistence has no storage generation")
	}
	dir, err := filesystem.sessionDir(ref.SessionID, true)
	if err != nil {
		return "", err
	}
	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	if manifest.SessionID != ref.SessionID {
		return "", ErrStaleGeneration
	}
	identity, err := readStorageIdentity(dir, manifest)
	return identity.Generation, err
}
