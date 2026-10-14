package boot

import (
	"testing"

	"deepzora/internal/provider"
)

func TestSyntheticEntryUsesDeclaredImageModality(t *testing.T) {
	for _, tc := range []struct {
		name       string
		modalities []provider.ModelModality
		vision     bool
	}{
		{"unknown", nil, false},
		{"text", []provider.ModelModality{provider.ModalityText}, false},
		{"image", []provider.ModelModality{provider.ModalityText, provider.ModalityImage}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &staticResolver{catalog: []provider.Descriptor{{
				Ref: "plugin/demo/fake/x", Model: "x", ContextWindow: 64_000,
				InputModalities: tc.modalities,
			}}}
			entry := syntheticEntryFromResolver(resolver, "plugin/demo/fake/x")
			if entry == nil || entry.Vision != tc.vision || entry.ContextWindow != 64_000 {
				t.Fatalf("synthetic entry = %+v, want vision=%v", entry, tc.vision)
			}
		})
	}
}
