// Package update defines the desktop auto-updater's shared types and signature
// verification — the single source of truth for the latest.json manifest format,
// the platform-asset lookup, and minisign verification. Both the CI signing tool
// (desktop/cmd/sign) and the running updater (desktop/updater.go) import this
// package, so the sign path and the verify path can never drift apart.
package update

import (
	"runtime"
	"strings"

	"deepzora/internal/releaseassets"
)

// ElectronInstallLayout identifies the current Electron application layout.
const ElectronInstallLayout = "electron-v1"

// Manifest is the latest.json published alongside a desktop release. The updater
// fetches it from the R2 mirror (primary) or GitHub releases (fallback), compares
// Version against the running build, and looks up the running platform's artifact
// via Asset / NativePackage.
type Manifest struct {
	ArtifactNaming  string           `json:"artifact_naming,omitempty"` // versioned-v1 retains legacy aliases for v0.0.1 clients
	Version         string           `json:"version"`                   // release version, e.g. "v1.1.0"
	Notes           string           `json:"notes"`                     // markdown release notes
	PubDate         string           `json:"pub_date"`                  // RFC3339, optional
	DownloadPage    string           `json:"download_page"`             // human-facing download page (macOS manual-update fallback)
	ReleaseNotesURL string           `json:"release_notes_url"`         // required exact version history page
	Platforms       map[string]Asset `json:"platforms"`                 // keyed by PlatformKey, e.g. "darwin-arm64"
	NativePackages  map[string]Asset `json:"native_packages,omitempty"` // optional OS package assets, e.g. linux-amd64 → .deb
	Downloads       map[string]Asset `json:"downloads,omitempty"`       // optional signed human-download assets keyed by exact filename
}

// Asset is one platform's downloadable artifact plus the metadata the updater
// needs to verify and report on it.
type Asset struct {
	URL    string `json:"url"`    // direct download URL for the artifact
	Sig    string `json:"sig"`    // URL of the detached minisign (.minisig) signature
	Size   int64  `json:"size"`   // artifact size in bytes (download-progress denominator)
	SHA256 string `json:"sha256"` // lowercase hex digest, for a second integrity check after verify
	// InstallLayout identifies the on-disk layout the artifact expects after
	// install. The current manifest must declare a supported layout explicitly.
	InstallLayout string `json:"install_layout"`
}

// PlatformKey is the map key used in Manifest.Platforms for the given OS/arch.
// The updater calls CurrentPlatform; the manifest generator builds keys the same
// way, so lookups always agree.
func PlatformKey(goos, goarch string) string { return goos + "-" + goarch }

// CurrentPlatform is PlatformKey for the running binary.
func CurrentPlatform() string { return PlatformKey(runtime.GOOS, runtime.GOARCH) }

// Asset returns the portable/tarball artifact for the running platform, if listed.
func (m Manifest) Asset() (Asset, bool) {
	a, ok := m.Platforms[CurrentPlatform()]
	return m.namedAsset(a), ok
}

// NativePackage returns the OS package artifact for the running platform, if listed.
// Older manifests omit native_packages; callers must treat absence as "no package".
func (m Manifest) NativePackage() (Asset, bool) {
	if m.NativePackages == nil {
		return Asset{}, false
	}
	a, ok := m.NativePackages[CurrentPlatform()]
	return m.namedAsset(a), ok
}

// Legacy maps stay intact on the wire for the already shipped v0.0.1 client.
func (m Manifest) namedAsset(a Asset) Asset {
	if m.ArtifactNaming != "versioned-v1" {
		return a
	}
	for _, spec := range releaseassets.DesktopAssets() {
		if strings.HasSuffix(a.URL, "/"+spec.Legacy) {
			if named, ok := m.Downloads[releaseassets.DesktopName(m.Version, spec)]; ok {
				return named
			}
		}
	}
	return a
}
