package config

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrModelConnectionChanged marks a saved selection whose migrated OpenCode
// Go connection no longer matches; a new explicit selection resolves it.
var ErrModelConnectionChanged = errors.New("MODEL_CONNECTION_CHANGED")

// ResolveSelectedModel resolves a current selection.
func (c *Config) ResolveSelectedModel(ref string) (string, error) {
	if entry, ok := c.resolveCurrentModel(ref); ok {
		return entry.Name + "/" + entry.Model, nil
	}
	return ref, nil
}

// ModelSelectionIdentity records only a digest, never resolved credentials.
// Current selections capture endpoint and model as well as the transport
// identity, so a later resume cannot silently adopt a different connection.
func (c *Config) ModelSelectionIdentity(ref string) string {
	entry, ok := c.resolveCurrentModel(ref)
	if !ok {
		return ""
	}
	tracked := isOpenCodeGoEntry(entry)
	if !tracked {
		return ""
	}
	b, _ := json.Marshal([]string{entry.Name, entry.Model, entry.Kind, entry.BaseURL, entry.RequestURL, openCodeGoIdentity(*entry)})
	return openCodeGoDigest(b)
}

// ResolveSavedModel uses an explicitly persisted choice when present; legacy
// sidecars without that choice retain the original migration protection.
func (c *Config) ResolveSavedModel(ref, identity string) (string, error) {
	if identity == "" {
		return c.ResolveSelectedModel(ref)
	}
	if current := c.ModelSelectionIdentity(ref); current == "" || current != identity {
		return "", fmt.Errorf("%w: saved connection for %q has changed; explicitly select a model to use the current connection", ErrModelConnectionChanged, ref)
	}
	entry, _ := c.resolveCurrentModel(ref)
	return entry.Name + "/" + entry.Model, nil
}

func (c *Config) resolveOpenCodeGoAutomaticSearch(current *ProviderEntry, resolve func(*ProviderEntry) *ProviderEntry) *ProviderEntry {
	if isOpenCodeGoEntry(current) {
		for i := range c.Providers {
			if openCodeGoIdentity(c.Providers[i]) != openCodeGoIdentity(*current) || !isOpenCodeGoEntry(&c.Providers[i]) {
				continue
			}
			entry, ok := c.ResolveModel(c.Providers[i].Name)
			if ok {
				if selected := resolve(entry); selected != nil {
					return selected
				}
			}
		}
	}
	return nil
}
