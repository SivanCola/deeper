package boot

import (
	"deepzora/internal/config"
)

func handleConfigLoadWarnings(opts Options, cfg *config.Config) bool {
	if cfg == nil || !cfg.HasLoadWarnings() || opts.OnConfigLoadWarnings == nil {
		return false
	}
	return opts.OnConfigLoadWarnings(cfg.LoadWarnings())
}
