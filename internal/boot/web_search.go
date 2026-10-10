package boot

import (
	"slices"

	"deeper/internal/config"
	"deeper/internal/event"
	"deeper/internal/i18n"
	"deeper/internal/netclient"
	"deeper/internal/provider"
	"deeper/internal/tool"
	"deeper/internal/websearch"
)

func addWebSearch(reg *tool.Registry, cfg *config.Config, current *config.ProviderEntry, proxy netclient.ProxySpec, sink event.Sink) {
	if cfg.Environment.Offline || (len(cfg.Tools.Enabled) > 0 && !slices.Contains(cfg.Tools.Enabled, "web_search")) {
		return
	}
	selection := cfg.ResolveWebSearch(current)
	if selection.Status == "invalid" && sink != nil {
		sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Code: "web_search_model_unavailable", Text: i18n.M.SearchModelUnavailable + " " + selection.Reason})
	}
	entry := selection.Entry
	if entry == nil {
		return
	}
	reg.Add(&websearch.Tool{
		ReportSourcesStatus: func(status string) {
			if sink != nil && status == provider.SourcesNotProvided {
				sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Code: "search_sources_not_provided", Text: i18n.M.SearchSourcesNotProvided})
			}
		},
		Factory: func() (provider.Provider, error) {
			return newProviderWithSearchMode(entry, proxy, nil, false)
		},
		ReportUsage: func(usage *provider.Usage) {
			if sink != nil {
				sink.Emit(event.Event{Kind: event.Usage, ModelRef: modelRefFromEntry(entry), Usage: usage, Pricing: entry.Price, UsageSource: "web-search"})
			}
		},
	})
}
