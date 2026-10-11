package main

import (
	"deepzora/internal/config"
	"reflect"
	"strings"
)

// Dedicated channel panels project the same current connection records as the
// generic connection editor. They never persist a second channel identity.
func botConfigWithConnectionViews(b config.BotConfig) config.BotConfig {
	b.QQ.Enabled = false
	b.QQ.AppID = ""
	b.QQ.AppSecretEnv = ""
	b.QQ.Model = ""
	b.QQ.ToolApprovalMode = ""
	b.QQ.WorkspaceRoot = ""
	b.Dingtalk.Enabled = false
	b.Dingtalk.ClientID = ""
	b.Dingtalk.SecretEnv = ""
	b.Dingtalk.Model = ""
	b.Dingtalk.ToolApprovalMode = ""
	b.Dingtalk.WorkspaceRoot = ""
	seen := map[string]bool{}
	for _, conn := range b.Connections {
		if seen[conn.Provider] {
			continue
		}
		seen[conn.Provider] = true
		switch conn.Provider {
		case "qq":
			b.QQ.Enabled = conn.Enabled
			b.QQ.AppID = conn.Credential.AppID
			b.QQ.AppSecretEnv = conn.Credential.AppSecretEnv
			b.QQ.Model = conn.Model
			b.QQ.ToolApprovalMode = conn.ToolApprovalMode
			b.QQ.WorkspaceRoot = conn.WorkspaceRoot
			b.QQ.Access = conn.Access
		case "dingtalk":
			b.Dingtalk.Enabled = conn.Enabled
			b.Dingtalk.ClientID = conn.Credential.AppID
			b.Dingtalk.SecretEnv = conn.Credential.AppSecretEnv
			b.Dingtalk.Model = conn.Model
			b.Dingtalk.ToolApprovalMode = conn.ToolApprovalMode
			b.Dingtalk.WorkspaceRoot = conn.WorkspaceRoot
			b.Dingtalk.Access = conn.Access
		}
	}
	return b
}

func updateDedicatedBotConnections(b, previous config.BotConfig) []config.BotConnectionConfig {
	previous = botConfigWithConnectionViews(previous)
	out := append([]config.BotConnectionConfig(nil), b.Connections...)
	update := func(platform, appID, secretEnv, model, mode, workspace string, enabled bool, access config.BotAccessConfig) {
		i := -1
		for j, c := range out {
			if c.Provider == platform {
				i = j
				break
			}
		}
		if i < 0 && strings.TrimSpace(appID) == "" {
			return
		}
		if i < 0 {
			out = append(out, config.BotConnectionConfig{ID: platform, Provider: platform, Domain: platform})
			i = len(out) - 1
		}
		c := &out[i]
		c.Enabled = enabled
		c.Credential.AppID = appID
		c.Credential.AppSecretEnv = secretEnv
		c.Model = model
		c.ToolApprovalMode = mode
		c.WorkspaceRoot = workspace
		c.Access = access
	}
	if !reflect.DeepEqual(b.QQ, previous.QQ) {
		update("qq", b.QQ.AppID, b.QQ.AppSecretEnv, b.QQ.Model, b.QQ.ToolApprovalMode, b.QQ.WorkspaceRoot, b.QQ.Enabled, b.QQ.Access)
	}
	if !reflect.DeepEqual(b.Dingtalk, previous.Dingtalk) {
		update("dingtalk", b.Dingtalk.ClientID, b.Dingtalk.SecretEnv, b.Dingtalk.Model, b.Dingtalk.ToolApprovalMode, b.Dingtalk.WorkspaceRoot, b.Dingtalk.Enabled, b.Dingtalk.Access)
	}
	return out
}
