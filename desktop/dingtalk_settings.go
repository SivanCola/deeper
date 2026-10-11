package main

import (
	"strings"

	"deepzora/internal/config"
)

func dingtalkConfigFromView(view DingtalkBotView) config.DingtalkBotConfig {
	return config.DingtalkBotConfig{
		Enabled:          view.Enabled,
		ClientID:         strings.TrimSpace(view.ClientID),
		SecretEnv:        strings.TrimSpace(view.ClientSecretEnv),
		BotName:          strings.TrimSpace(view.BotName),
		RequireMention:   view.RequireMention,
		Model:            strings.TrimSpace(view.Model),
		ToolApprovalMode: normalizeBotConnectionToolApprovalMode(view.ToolApprovalMode),
		WorkspaceRoot:    strings.TrimSpace(view.WorkspaceRoot),
		Access:           botAccessConfigFromView(view.Access),
	}
}
