package plugin

import "deeper/internal/tool"

func findToolByName(tools []tool.Tool, name string) tool.Tool {
	for _, candidate := range tools {
		if candidate.Name() == name {
			return candidate
		}
	}
	return nil
}
