package cli

import (
	"fmt"
	"strings"

	"deeper/internal/config"
	"deeper/internal/pluginpkg"
)

func pluginArgNames() []string {
	names, err := pluginpkg.InstalledNames(config.DeeperHomeDir())
	if err != nil {
		return nil
	}
	return names
}

func (m *chatTUI) runPluginSubcommand(input string) {
	args := tokenizeArgs(input)
	sub := ""
	if len(args) > 1 {
		sub = strings.ToLower(args[1])
	}
	switch sub {
	case "", "list", "ls":
		text, err := pluginpkg.InstalledListText(config.DeeperHomeDir())
		if err != nil {
			m.notice("plugins: " + err.Error())
			return
		}
		m.commitLine(text)
	case "show", "cat":
		if len(args) < 3 {
			m.notice("usage: /plugins show <name>")
			return
		}
		text, err := pluginpkg.InstalledShowText(config.DeeperHomeDir(), args[2])
		if err != nil {
			m.notice("plugins: " + err.Error())
			return
		}
		m.commitLine(text)
	default:
		m.notice(fmt.Sprintf("unknown /plugins subcommand %s - try: /plugins or /plugins show <name>", args[1]))
	}
}
