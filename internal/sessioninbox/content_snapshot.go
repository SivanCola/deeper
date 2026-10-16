package sessioninbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"deepzora/internal/attachment"
	filelock "deepzora/internal/identitylock"
	"deepzora/internal/sessioncontent"
)

// FreezeSnapshot excludes queue transactions while a caller reads or copies
// manifest-reachable blobs. An absent inbox stays absent.
func FreezeSnapshot(ctx context.Context, dir string) (func(), error) {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("sessioninbox: snapshot source is not a directory")
	}
	return filelock.Acquire(ctx, filepath.Join(dir, diskLockName))
}

// FrozenContentRefs reads only manifest-reachable blobs. The caller owns the
// disk transaction lock or an exclusive copied snapshot; no state is created.
func FrozenContentRefs(dir string) ([]sessioncontent.Ref, error) {
	data, err := readRegularFile(filepath.Join(dir, manifestName), maxManifestBytes)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	man, err := decodeManifest(data)
	if err != nil {
		return nil, err
	}
	if man.SchemaVersion > SchemaVersion {
		return nil, ErrSchemaReadonly
	}
	reader := &Store{dir: dir, limits: (Limits{}).withDefaults()}
	var refs []sessioncontent.Ref
	for _, item := range man.Items {
		env, err := reader.readBlobLocked(blobNameFor(item), item.Checksum)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(env)
		if err != nil {
			return nil, err
		}
		refs = append(refs, attachment.CollectJSONRefs(body)...)
	}
	return refs, nil
}
