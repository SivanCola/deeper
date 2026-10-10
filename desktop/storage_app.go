package main

import (
	"os"

	"deeper/internal/config"
	"deeper/internal/pluginpkg"
)

type StorageSettingsView struct {
	DefaultWorkspace string `json:"defaultWorkspace"`
	StatePath        string `json:"statePath"`
	CachePath        string `json:"cachePath"`
	ExtensionsPath   string `json:"extensionsPath"`
}

func (a *App) StorageSettings() StorageSettingsView {
	return StorageSettingsView{
		DefaultWorkspace: storageDefaultWorkspace(),
		StatePath:        config.MemoryUserDir(),
		CachePath:        config.CacheDir(),
		ExtensionsPath:   pluginpkg.PluginsDir(config.DeeperHomeDir()),
	}
}

func storageDefaultWorkspace() string {
	if workspace := loadWorkspace(); workspace != "" {
		if info, err := os.Stat(workspace); err == nil && info.IsDir() {
			return workspace
		}
	}
	workspace, _ := os.Getwd()
	return workspace
}
