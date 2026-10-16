package acp

import (
	"strings"

	"deepzora/internal/extension/providerext"
	"deepzora/internal/provider"
)

// enrichStateWithExtensionModels folds a live controller's merged provider
// catalog into a session config state, so plugin/... models contributed by
// installed extensions are discoverable in the category:"model" config option
// even when none is
// already the current model. The state is otherwise config-derived: config
// entries keep precedence and extension refs only fill gaps. A nil catalog
// (no extension sidecars, or a controller without one) is a no-op.
func enrichStateWithExtensionModels(state SessionConfigState, catalog []provider.Descriptor) SessionConfigState {
	// Collect the extension refs first: when the config backend gave no model
	// state at all, extension models still deserve a selector.
	type extModel struct{ ref, name string }
	var ext []extModel
	seen := map[string]bool{}
	for _, desc := range catalog {
		ref := strings.TrimSpace(desc.Ref)
		if ref == "" || providerext.PluginRefOwner(ref) == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		name := strings.TrimSpace(desc.DisplayName)
		if name == "" {
			name = ref
		}
		ext = append(ext, extModel{ref, name})
	}
	if len(ext) == 0 {
		return state
	}
	hasModelOption := false
	for _, m := range ext {
		for i := range state.ConfigOptions {
			opt := &state.ConfigOptions[i]
			if opt.Category != "model" {
				continue
			}
			hasModelOption = true
			if !hasSelectOption(opt.Options, m.ref) {
				opt.Options = append(opt.Options, SessionConfigSelectOption{
					Value:       m.ref,
					Name:        m.name,
					Description: m.name,
				})
			}
		}
	}
	if !hasModelOption {
		options := make([]SessionConfigSelectOption, 0, len(ext))
		for _, m := range ext {
			options = append(options, SessionConfigSelectOption{Value: m.ref, Name: m.name, Description: m.name})
		}
		state.ConfigOptions = append(state.ConfigOptions, SessionConfigOption{
			ID: "model", Name: "Model", Category: "model", Type: "select",
			CurrentValue: state.Model,
			Options:      options,
		})
	}
	return state
}

func hasSelectOption(options []SessionConfigSelectOption, ref string) bool {
	for _, o := range options {
		if o.Value == ref {
			return true
		}
	}
	return false
}
