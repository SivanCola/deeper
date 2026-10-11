package config

import (
	"deepzora/internal/provider"
	"maps"
	"reflect"
)

// ProviderEntryConfigSnapshot strips process-only state from a provider copy so
// optimistic edit logs contain only persisted configuration.
func ProviderEntryConfigSnapshot(entry ProviderEntry) ProviderEntry {
	stripRuntimeReasoningDefaults(&entry)
	if len(entry.Models) == 0 && entry.Model != "" {
		entry.Models = []string{entry.Model}
	}
	entry.Prices = maps.Clone(entry.Prices)
	if entry.Price != nil && entry.Model != "" && entry.Prices[entry.Model] == nil {
		if entry.Prices == nil {
			entry.Prices = make(map[string]*provider.Pricing)
		}
		entry.Prices[entry.Model] = clonePricing(entry.Price)
	}
	entry.ModelOverrides = maps.Clone(entry.ModelOverrides)

	entry.Model = ""
	entry.Price = nil
	entry.Vision = false
	entry.VisionModels = nil
	entry.resolvedAPIKey = ""
	entry.resolvedSource = CredentialSource{}
	entry.visionOverride = nil
	entry.persistedOfficialCurrency = ""
	return entry
}

// ProviderEntriesConfigEqual compares persisted provider configuration while
// ignoring credentials and capability state resolved only for the current
// process. Setup uses it for optimistic conflict detection during replay.
func ProviderEntriesConfigEqual(a, b ProviderEntry) bool {
	return reflect.DeepEqual(ProviderEntryConfigSnapshot(a), ProviderEntryConfigSnapshot(b))
}
