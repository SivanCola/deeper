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
	BuildName string `json:"buildName"`
	Platform  string `json:"platform"`
	Channel   string `json:"channel,omitempty"`
	Suffix    string `json:"suffix"`
}

func DesktopAssets() []DesktopAsset {
	var assets []DesktopAsset
	if err := json.Unmarshal(desktopJSON, &assets); err != nil {
		panic(err)
	}
	return assets
}

// DesktopName is the public filename for every supported release.
func DesktopName(version string, asset DesktopAsset) string {
	version = strings.TrimPrefix(version, "v")
	return "Deepzora-" + version + "-" + asset.Suffix
}
