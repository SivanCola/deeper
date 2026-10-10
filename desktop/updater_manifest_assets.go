package main

type requiredDesktopAsset struct {
	group    string
	key      string
	filename string
}

var (
	requiredDesktopUpdaterAssets = []requiredDesktopAsset{
		{group: "platforms", key: "darwin-arm64", filename: "Deeper-darwin-arm64.zip"},
		{group: "platforms", key: "darwin-amd64", filename: "Deeper-darwin-amd64.zip"},
		{group: "platforms", key: "windows-amd64", filename: "Deeper-windows-amd64-installer.exe"},
		{group: "platforms", key: "windows-arm64", filename: "Deeper-windows-arm64-installer.exe"},
		{group: "platforms", key: "linux-amd64", filename: "Deeper-linux-amd64.tar.gz"},
		{group: "native_packages", key: "linux-amd64", filename: "Deeper-linux-amd64.deb"},
	}
	legacyDesktopDownloadAssets = []requiredDesktopAsset{
		{group: "downloads", key: "Deeper-darwin-universal.dmg", filename: "Deeper-darwin-universal.dmg"},
		{group: "downloads", key: "Deeper-windows-amd64.zip", filename: "Deeper-windows-amd64.zip"},
	}
	requiredDesktopDownloadAssets = append(append([]requiredDesktopAsset(nil), legacyDesktopDownloadAssets...),
		requiredDesktopAsset{group: "downloads", key: "Deeper-darwin-arm64.dmg", filename: "Deeper-darwin-arm64.dmg"},
		requiredDesktopAsset{group: "downloads", key: "Deeper-darwin-amd64.dmg", filename: "Deeper-darwin-amd64.dmg"},
	)
)
