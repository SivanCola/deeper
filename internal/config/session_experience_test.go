package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestSetDesktopSessionExperience(t *testing.T) {
	tests := []struct {
		mode      string
		reasoning string
	}{
		{mode: "standard", reasoning: "auto"},
		{mode: "deep", reasoning: "expanded"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			cfg := Default()
			if err := cfg.SetDesktopSessionExperience(tt.mode); err != nil {
				t.Fatalf("SetDesktopSessionExperience(%q): %v", tt.mode, err)
			}
			if got := cfg.DesktopSessionExperience(); got != tt.mode {
				t.Fatalf("experience = %q, want %q", got, tt.mode)
			}
		})
	}
	if err := Default().SetDesktopSessionExperience("invalid"); err == nil {
		t.Fatal("invalid session experience unexpectedly accepted")
	}
}

func TestDesktopSessionExperienceRenderRoundTrip(t *testing.T) {
	cfg := Default()
	if err := cfg.SetDesktopSessionExperience("deep"); err != nil {
		t.Fatal(err)
	}
	rendered := RenderTOML(cfg)
	if !strings.Contains(rendered, `session_experience = "deep"`) {
		t.Fatalf("rendered config missing canonical experience:\n%s", rendered)
	}
	var decoded Config
	if _, err := toml.Decode(rendered, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.DesktopSessionExperience(); got != "deep" {
		t.Fatalf("round-tripped experience = %q, want deep", got)
	}
}
