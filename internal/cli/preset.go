package cli

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
	"deepzora/internal/i18n"
)

func (m *chatTUI) runPresetCommand(input string) tea.Cmd {
	args := tokenizeArgs(input)
	if len(args) == 1 {
		args = append(args, "current")
	}
	if len(args) < 2 || (args[1] != "list" && args[1] != "current" && args[1] != "select") ||
		(args[1] == "select" && len(args) != 3) || (args[1] != "select" && len(args) != 2) {
		m.notice(i18n.M.PresetUsage)
		return nil
	}
	presets, ok := m.ctrl.(control.AgentPresets)
	if !ok {
		m.notice(i18n.M.PresetUnavailable)
		return nil
	}
	list, err := presets.ListAgentPresets()
	if err != nil {
		m.notice("preset: " + err.Error())
		return nil
	}
	switch args[1] {
	case "current":
		m.notice(fmt.Sprintf(i18n.M.PresetCurrentFmt, list.Current))
		if list.Locked {
			m.notice(i18n.M.PresetLocked)
		}
	case "list":
		for _, preset := range list.Presets {
			current := ""
			if preset.ID == list.Current {
				current = " *"
			}
			m.notice(fmt.Sprintf("%s%s [%s] — %s", preset.ID, current, preset.Scope, preset.Description))
		}
		for _, problem := range list.Problems {
			m.notice("preset: " + problem)
		}
	case "select":
		if list.Locked {
			m.notice(i18n.M.PresetLocked)
			return nil
		}
		id := agentpreset.NormalizeID(args[2])
		if id == list.Current {
			m.notice(fmt.Sprintf(i18n.M.PresetCurrentFmt, id))
			return nil
		}
		if m.rebuildRuntime == nil {
			m.notice(i18n.M.RuntimeRefreshUnavailable)
			return nil
		}
		if m.runtimeSwitchBusy() {
			m.notice(i18n.M.RuntimeRefreshBusy)
			return nil
		}
		if m.modelSwitchPending {
			m.notice(i18n.M.RuntimeSwitchPending)
			return nil
		}
		return m.schedulePresetSelection(id)
	}
	return nil
}

func (m *chatTUI) schedulePresetSelection(id string) tea.Cmd {
	old, ok := m.ctrl.(*control.Controller)
	if !ok {
		m.notice(i18n.M.RuntimeRefreshUnavailable)
		return nil
	}
	rebuild := m.rebuildRuntime
	spec := controllerBuildSpec{ModelRef: m.modelRef, AgentPresetOverride: &id}
	m.modelSwitchPending = true
	m.pendingModelSwitch = func() tea.Msg {
		result, err := rebuild(context.Background(), spec, old)
		if err != nil {
			return modelSwitchMsg{ref: spec.ModelRef, failurePrefix: "preset", err: err}
		}
		next := result.Controller
		return modelSwitchMsg{
			ref: spec.ModelRef, ctrl: next, oldCtrl: old,
			label: next.Label(), commands: next.Commands(), skills: next.SlashSkills(), host: next.Host(),
			successNotice: fmt.Sprintf(i18n.M.PresetSelectedFmt, id),
		}
	}
	return m.pendingModelSwitch
}

func (m *chatTUI) presetArgItems(val string) ([]compItem, int, bool) {
	from := strings.LastIndexAny(val, " \t") + 1
	prior := strings.Fields(val[:from])
	if len(prior) == 0 || prior[0] != "/preset" {
		return nil, 0, false
	}
	var items []compItem
	if len(prior) == 1 {
		items = []compItem{
			{label: "list", insert: "list", hint: i18n.M.CmdPreset},
			{label: "current", insert: "current", hint: i18n.M.CmdPreset},
			{label: "select", insert: "select ", hint: i18n.M.CmdPreset, descend: true},
		}
	} else if len(prior) == 2 && prior[1] == "select" {
		if source, ok := m.ctrl.(control.AgentPresets); ok {
			if list, err := source.ListAgentPresets(); err == nil && !list.Locked {
				for _, preset := range list.Presets {
					items = append(items, compItem{label: preset.ID, insert: preset.ID, hint: preset.Description})
				}
			}
		}
	}
	prefix := strings.ToLower(val[from:])
	out := items[:0]
	for _, item := range items {
		if strings.HasPrefix(item.label, prefix) {
			out = append(out, item)
		}
	}
	return out, from, true
}
