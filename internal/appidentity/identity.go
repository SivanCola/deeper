package appidentity

const (
	// AppUserModelID belongs to Desktop, independently of installed Studio versions.
	// Keep it stable across upgrades and aligned with the Electron shell.
	AppUserModelID = "io.github.sivancola.deeper"
	DisplayName    = "Deeper"

	// Old Desktop and Wails Studio shared this ID; ownership must precede migration.
	legacyAppUserModelID      = "Deeper"
	studioAppUserModelID      = "io.github.sivancola.deeper.studio"
	legacyTauriAppUserModelID = "io.github.sivancola.deeper.dev"
)
