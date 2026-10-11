package pluginpkg

import (
	"fmt"
	"sort"
)

func LoadInstalled(deepzoraHome string) ([]InstalledPackage, []string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	st, err := LoadState(deepzoraHome)
	if err != nil {
		return nil, []string{err.Error()}
	}
	var out []InstalledPackage
	var warnings []string
	stateChanged := false
	for i := range st.Plugins {
		installed := st.Plugins[i]
		if !installed.Enabled {
			continue
		}
		root := ResolveRoot(deepzoraHome, installed.Root)
		pkg, pkgWarnings, err := ParseDir(root)
		if err != nil {
			reason := err.Error()
			if st.Plugins[i].Status != PluginStatusDisabledIncompatible || st.Plugins[i].StatusReason != reason {
				st.Plugins[i].Status = PluginStatusDisabledIncompatible
				st.Plugins[i].StatusReason = reason
				stateChanged = true
			}
			remediation := "repair or reinstall the plugin"
			warnings = append(warnings, fmt.Sprintf("%s: %s: %v; remediation: %s", installed.Name, PluginStatusDisabledIncompatible, err, remediation))
			continue
		}
		if st.Plugins[i].Status != "" || st.Plugins[i].StatusReason != "" {
			st.Plugins[i].Status = ""
			st.Plugins[i].StatusReason = ""
			stateChanged = true
		}
		out = append(out, InstalledPackage{Installed: installed, Package: pkg, Warnings: pkgWarnings})
	}
	if stateChanged {
		if err := SaveState(deepzoraHome, st); err != nil {
			warnings = append(warnings, "persist plugin compatibility status: "+err.Error())
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Installed.Name < out[j].Installed.Name })
	return out, warnings
}
