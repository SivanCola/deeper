package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	productcontent "deepzora/docs"
	"deepzora/internal/productdocs"
	releasenotes "deepzora/release-notes"
)

func TestDocsManifestVerifiesBundledSourceBytes(t *testing.T) {
	manifest, err := productdocs.EmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*testing.T, string)
		want   int
	}{
		{"unbundled draft is ignored", func(t *testing.T, root string) {
			writeDocsManifestFixture(t, filepath.Join(root, "docs", "INTERNAL_BUILD_PLAN.md"), []byte("# Draft\n\nNot shipped.\n"))
		}, 0},
		{"bundled Markdown drift is rejected", func(t *testing.T, root string) {
			appendDocsManifestFixture(t, filepath.Join(root, "docs", "GUIDE.md"))
		}, 1},
		{"missing bundled Markdown is rejected", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "docs", "GUIDE.md")); err != nil {
				t.Fatal(err)
			}
		}, 1},
		{"release catalog drift is rejected", func(t *testing.T, root string) {
			appendDocsManifestFixture(t, filepath.Join(root, "release-notes", "releases.json"))
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			entries, err := fs.ReadDir(productcontent.Content, ".")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				data, err := productcontent.Content.ReadFile(entry.Name())
				if err != nil {
					t.Fatal(err)
				}
				writeDocsManifestFixture(t, filepath.Join(root, "docs", entry.Name()), data)
			}
			catalog, err := releasenotes.Content.ReadFile("releases.json")
			if err != nil {
				t.Fatal(err)
			}
			writeDocsManifestFixture(t, filepath.Join(root, "release-notes", "releases.json"), catalog)
			tc.change(t, root)
			if got := docsManifestCommand([]string{"--verify-source", root}, manifest.Version); got != tc.want {
				t.Fatalf("docsManifestCommand = %d, want %d", got, tc.want)
			}
		})
	}
}

func writeDocsManifestFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendDocsManifestFixture(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeDocsManifestFixture(t, path, append(data, '\n'))
}
