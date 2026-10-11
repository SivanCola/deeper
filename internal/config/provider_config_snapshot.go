package config

// providerConfigSnapshots strips selected-model runtime caches and emits only
// the current provider configuration fields.
func providerConfigSnapshots(entries []ProviderEntry) []ProviderEntry {
	result := make([]ProviderEntry, len(entries))
	for i, entry := range entries {
		result[i] = ProviderEntryConfigSnapshot(entry)
	}
	return result
}
