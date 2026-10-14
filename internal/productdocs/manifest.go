package productdocs

import (
	"io/fs"

	productcontent "deepzora/docs"
)

// SourceManifest computes the corpus identity from the source Markdown and
// structured release catalog used by a build.
func SourceManifest(docsFS, releaseNotesFS fs.FS) (Manifest, error) {
	c, err := loadCatalogWithReleaseNotes(docsFS, releaseNotesFS)
	if err != nil {
		return Manifest{}, err
	}
	return c.manifest(), nil
}

// SourceManifestForEmbedded reads source bytes for the exact document set in
// this binary. Unbundled design notes and maintainer drafts are not inputs to
// its corpus identity; every bundled file must still exist and match.
func SourceManifestForEmbedded(docsFS, releaseNotesFS fs.FS) (Manifest, error) {
	entries, err := fs.ReadDir(productcontent.Content, ".")
	if err != nil {
		return Manifest{}, err
	}
	c, err := loadCatalogEntries(docsFS, releaseNotesFS, entries)
	if err != nil {
		return Manifest{}, err
	}
	return c.manifest(), nil
}
