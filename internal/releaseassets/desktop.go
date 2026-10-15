// Package releaseassets defines the public Desktop download names.
package releaseassets

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed desktop.json
var desktopJSON []byte

type DesktopAsset struct {
	Legacy string `json:"legacy"`
	Suffix string `json:"suffix"`
}

func DesktopAssets() []DesktopAsset {
	var assets []DesktopAsset
	if err := json.Unmarshal(desktopJSON, &assets); err != nil {
		panic(err)
	}
	return assets
}

// DesktopName preserves the immutable first release's download URLs.
func DesktopName(version string, asset DesktopAsset) string {
	version = strings.TrimPrefix(version, "v")
	if version == "0.0.1" {
		return asset.Legacy
	}
	return "Deepzora-" + version + "-" + asset.Suffix
}
