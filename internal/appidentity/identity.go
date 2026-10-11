package appidentity

const (
	// AppUserModelID belongs to Desktop, independently of installed Studio versions.
	// Keep it stable across upgrades and aligned with the Electron shell.
	AppUserModelID = "com.deepzora.desktop"
	DisplayName    = "Deepzora"

	// Old Desktop and Wails Studio shared this ID; ownership must precede migration.
	legacyAppUserModelID      = "Deepzora"
	studioAppUserModelID      = "com.deepzora.desktop.studio"
	legacyTauriAppUserModelID = "com.deepzora.desktop.dev"
)
