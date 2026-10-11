package config

import (
	"fmt"
	"strings"
)

// SessionExperience is the single user-facing desktop presentation preference.
type SessionExperience string

const (
	SessionExperienceStandard SessionExperience = "standard"
	SessionExperienceDeep     SessionExperience = "deep"
)

// DesktopSessionExperience returns the current desktop presentation preference.
func (c *Config) DesktopSessionExperience() string {
	if c == nil {
		return string(SessionExperienceStandard)
	}
	switch strings.ToLower(strings.TrimSpace(c.Desktop.SessionExperience)) {
	case string(SessionExperienceDeep):
		return string(SessionExperienceDeep)
	case string(SessionExperienceStandard):
		return string(SessionExperienceStandard)
	default:
		return string(SessionExperienceStandard)
	}
}

// SetDesktopSessionExperience persists the canonical desktop presentation
// preference.
func (c *Config) SetDesktopSessionExperience(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case string(SessionExperienceStandard):
		c.Desktop.SessionExperience = string(SessionExperienceStandard)
		return nil
	case string(SessionExperienceDeep):
		c.Desktop.SessionExperience = string(SessionExperienceDeep)
		return nil
	default:
		return fmt.Errorf("session experience %q: must be standard|deep", mode)
	}
}

func renderDesktopSessionExperience(b *strings.Builder, c *Config) {
	if strings.TrimSpace(c.Desktop.SessionExperience) != "" {
		fmt.Fprintf(b, "session_experience = %q   # desktop: standard|deep transcript experience\n", c.DesktopSessionExperience())
	}
}
