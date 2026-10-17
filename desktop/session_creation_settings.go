package main

import (
	"deepzora/internal/config"
	"encoding/json"
	"strings"
)

type SessionCreationSettings struct {
	Model             string                `json:"model"`
	AgentPreset       string                `json:"agentPreset,omitempty"`
	ModelSource       string                `json:"modelSource,omitempty"`
	Effort            string                `json:"effort,omitempty"`
	Mode              string                `json:"mode"`
	CollaborationMode string                `json:"collaborationMode,omitempty"`
	ToolApprovalMode  string                `json:"toolApprovalMode"`
	Goal              string                `json:"goal,omitempty"`
	DisabledMCP       map[string]ServerView `json:"disabledMcp"`
	MCPOrder          []string              `json:"mcpOrder"`
}

func (a *App) defaultSessionSettings(scope, workspaceRoot string) SessionCreationSettings {
	actualRoot := workspaceRoot
	if scope != "project" {
		scope, actualRoot = "global", globalWorkspaceRoot()
	}
	model, approval := desktopNewSessionDefaults(scope, actualRoot)
	settings := SessionCreationSettings{Model: model, ModelSource: "default",
		Mode: tabModeForPlan(false), ToolApprovalMode: approval,
		DisabledMCP: map[string]ServerView{}, MCPOrder: []string{}}
	a.mu.RLock()
	if active := a.activeTabLocked(); active != nil {
		if effort := config.RebindSessionEffort(nil, active.model, settings.Model, active.effort); effort != nil {
			settings.Effort = *effort
		}
		settings.DisabledMCP = cloneServerViewMap(active.disabledMCP)
		settings.MCPOrder = append([]string(nil), active.mcpOrder...)
	}
	a.mu.RUnlock()
	return settings
}

func sessionInputHasContent(contentJSON string) bool {
	trimmed := strings.TrimSpace(contentJSON)
	if trimmed == "" || trimmed == "{}" {
		return false
	}
	var content struct {
		Text             string            `json:"text"`
		GoalDraft        bool              `json:"goalDraft"`
		Invocations      []json.RawMessage `json:"invocations"`
		Attachments      []json.RawMessage `json:"attachments"`
		WorkspaceRefs    []json.RawMessage `json:"workspaceRefs"`
		PastedBlocks     []json.RawMessage `json:"pastedBlocks"`
		SessionRefs      []json.RawMessage `json:"sessionRefs"`
		SelectedTextRefs []json.RawMessage `json:"selectedTextRefs"`
	}
	if err := json.Unmarshal([]byte(trimmed), &content); err != nil {
		// Unknown shapes stay visible rather than silently hiding saved work.
		return true
	}
	return content.GoalDraft || strings.TrimSpace(content.Text) != "" || len(content.Invocations) > 0 || len(content.Attachments) > 0 ||
		len(content.WorkspaceRefs) > 0 || len(content.PastedBlocks) > 0 || len(content.SessionRefs) > 0 ||
		len(content.SelectedTextRefs) > 0
}

func (a *App) metaForSession(sessionID string) *TabMeta {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab != nil && tab.SessionID == sessionID {
			meta := enrichTabMeta(a.tabMeta(tab, tab.ID == a.activeTabID))
			return &meta
		}
	}
	return nil
}
