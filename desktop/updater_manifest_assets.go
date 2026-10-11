package main

type requiredDesktopAsset struct {
	group    string
	key      string
	filename string
}

var (
	requiredDesktopUpdaterAssets = []requiredDesktopAsset{
		{group: "platforms", key: "darwin-arm64", filename: "Deepzora-darwin-arm64.zip"},
		{group: "platforms", key: "darwin-amd64", filename: "Deepzora-darwin-amd64.zip"},
		{group: "platforms", key: "windows-amd64", filename: "Deepzora-windows-amd64-installer.exe"},
		{group: "platforms", key: "windows-arm64", filename: "Deepzora-windows-arm64-installer.exe"},
		{group: "platforms", key: "linux-amd64", filename: "Deepzora-linux-amd64.tar.gz"},
		{group: "native_packages", key: "linux-amd64", filename: "Deepzora-linux-amd64.deb"},
	}
	requiredDesktopDownloadAssets = []requiredDesktopAsset{
		{group: "downloads", key: "Deepzora-darwin-universal.dmg", filename: "Deepzora-darwin-universal.dmg"},
		{group: "downloads", key: "Deepzora-windows-amd64.zip", filename: "Deepzora-windows-amd64.zip"},
		{group: "downloads", key: "Deepzora-darwin-arm64.dmg", filename: "Deepzora-darwin-arm64.dmg"},
		{group: "downloads", key: "Deepzora-darwin-amd64.dmg", filename: "Deepzora-darwin-amd64.dmg"},
	}
)
