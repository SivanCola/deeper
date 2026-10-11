package session

import (
	"context"
	"path/filepath"
)

func (opts OpenOptions) openContext() context.Context {
	if opts.Context != nil {
		return opts.Context
	}
	return context.Background()
}

func loadStartupCatalogPreview(dir string, manifest Manifest, externalHistory bool, startup *startupSessionState) {
	if externalHistory && startup.catalogPreview == "" {
		revision, revisionErr := revisionOfLog(dir)
		cacheDir := filepath.Join(filepath.Dir(dir), ".query-cache", filepath.Base(dir))
		if revisionErr == nil {
			if metadata, metadataErr := readCatalogMetadata(cacheDir, manifest, revision); metadataErr == nil {
				startup.catalogPreview = metadata.Preview
			}
		}
	}
}
