package acp

import (
	"context"
	"testing"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestEnrichStateWithExtensionModels(t *testing.T) {
	base := SessionConfigState{
		Model: "openai/x",
		ConfigOptions: []SessionConfigOption{{
			ID: "model", Name: "Model", Category: "model", Type: "select",
			CurrentValue: "openai/x",
			Options:      []SessionConfigSelectOption{{Value: "openai/x", Name: "openai/x"}},
		}},
	}
	catalog := []provider.Descriptor{
		{Ref: "plugin/demo/fake/echo", DisplayName: "Demo Echo"},
		{Ref: "openai/x"},              // config-owned: skipped (not plugin-namespaced)
		{Ref: "plugin/demo/fake/echo"}, // duplicate: deduped
		{Ref: "plugin//fake/echo"},     // malformed owner: skipped
	}

	out := enrichStateWithExtensionModels(base, catalog)

	opts := out.ConfigOptions[0].Options
	if len(opts) != 2 || opts[1].Value != "plugin/demo/fake/echo" || opts[1].Name != "Demo Echo" || opts[1].Description != "Demo Echo" {
		t.Fatalf("model select options = %+v", opts)
	}

	fromEmpty := enrichStateWithExtensionModels(SessionConfigState{Model: "openai/x"}, catalog)
	if len(fromEmpty.ConfigOptions) != 1 || len(fromEmpty.ConfigOptions[0].Options) != 1 {
		t.Fatalf("duplicate extension refs added to empty selector: %+v", fromEmpty.ConfigOptions)
	}

	// Idempotent: enriching twice must not duplicate.
	twice := enrichStateWithExtensionModels(out, catalog)
	if len(twice.ConfigOptions[0].Options) != 2 {
		t.Fatalf("enrichment is not idempotent: %+v", twice.ConfigOptions)
	}

	// An empty catalog leaves the selectors unchanged.
	empty := enrichStateWithExtensionModels(SessionConfigState{Model: "openai/x"}, nil)
	if len(empty.ConfigOptions) != 0 {
		t.Fatal("nil catalog mutated the state")
	}
}

// A session whose current model is a builtin must still SEE installed
// extension models on a config-state read (the reviewer P1 case).
func TestConfigStateForSessionIncludesExtensionModels(t *testing.T) {
	ctrl := control.New(control.Options{
		Sink: event.Discard,
		ProviderResolver: &provider.StaticResolver{
			Descriptors: []provider.Descriptor{
				{Ref: "openai/x", DisplayName: "openai"},
				{Ref: "plugin/demo/fake/echo", DisplayName: "Demo Echo"},
			},
		},
	})
	defer ctrl.Close()

	svc := &service{}
	sess := &acpSession{id: "sess-1", cwd: "/tmp", ctrl: ctrl}
	state, err := svc.configStateForSession(context.Background(), sess)
	if err != nil {
		t.Fatalf("configStateForSession: %v", err)
	}
	var hasPlugin bool
	for _, option := range state.ConfigOptions {
		if option.Category == "model" && hasSelectOption(option.Options, "plugin/demo/fake/echo") {
			hasPlugin = true
		}
	}
	if !hasPlugin {
		t.Fatalf("extension model not discoverable on config-state read: %+v", state.ConfigOptions)
	}
}
