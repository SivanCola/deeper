package main

import (
	"fmt"
	"slices"
	"strings"

	"deepzora/internal/config"
)

// AddOfficialProviderAccess adds one curated desktop provider template to the
// Settings > Model > Access list. The runtime default providers still exist
// independently; this only records the user's explicit access setup.
func (a *App) AddOfficialProviderAccess(kind, key string) (string, error) {
	return a.applyModelConfigChangeWithWarning("provider access", func(c *config.Config) error { return addOfficialProviderAccessConfig(c, kind, key) })
}

func addOfficialProviderAccessConfig(c *config.Config, kind, key string) error {
	entries, keyEnv, err := officialProviderTemplate(kind)
	if err != nil {
		return err
	}
	if _, err := validateOfficialProviderAccessInstall(c, kind, entries, keyEnv); err != nil {
		return err
	}
	names, err := installOfficialProviderAccess(c, kind, entries)
	if err != nil {
		return err
	}
	if strings.TrimSpace(key) != "" {
		env, err := c.StageModelCredentialLocked(key)
		if err != nil {
			return err
		}
		for i := range c.Providers {
			if slices.Contains(names, c.Providers[i].Name) {
				c.Providers[i].APIKeyEnv = env
			}
		}
	}
	addProviderAccess(c, names...)
	return nil
}

func validateOfficialProviderAccessInstall(c *config.Config, kind string, entries []config.ProviderEntry, fallbackKeyEnv string) (string, error) {
	keyEnv := strings.TrimSpace(fallbackKeyEnv)
	wantKind := strings.ToLower(strings.TrimSpace(kind))
	for _, entry := range entries {
		existing, ok := c.Provider(strings.TrimSpace(entry.Name))
		if !ok {
			continue
		}
		if officialProviderKindFromEntry(*existing) != wantKind {
			return "", fmt.Errorf("official provider %q cannot be added because provider name %q already belongs to a custom endpoint; edit, rename, or remove the existing provider first", kind, entry.Name)
		}
		if env := strings.TrimSpace(existing.APIKeyEnv); env != "" {
			keyEnv = env
		}
	}
	return keyEnv, nil
}

func installOfficialProviderAccess(c *config.Config, kind string, entries []config.ProviderEntry) ([]string, error) {
	if c == nil {
		return nil, fmt.Errorf("config is nil")
	}
	wantKind := strings.ToLower(strings.TrimSpace(kind))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		existing, ok := c.Provider(name)
		if ok {
			if officialProviderKindFromEntry(*existing) != wantKind {
				return nil, fmt.Errorf("official provider %q cannot be added because provider name %q already belongs to a custom endpoint; edit, rename, or remove the existing provider first", kind, name)
			}
			if len(existing.ModelList()) == 0 {
				return nil, fmt.Errorf("provider %q has no current model catalog", name)
			}
		} else if err := c.UpsertProvider(entry); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}
