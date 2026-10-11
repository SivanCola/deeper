package config

import "testing"

func TestLoadedPermissionPresetsRejectHistoricalAndUnknownValues(t *testing.T) {
	for _, preset := range []string{"ask", "auto", "yolo", "full-access", "mystery-mode"} {
		c := Default()
		c.Desktop.DefaultToolApprovalMode = preset
		if err := normalizeLoadedConfig(c); err == nil {
			t.Fatalf("accepted unsupported preset %q", preset)
		}
	}
	for _, preset := range []string{"read-only", "workspace-write", "danger-full-access"} {
		c := Default()
		c.Desktop.DefaultToolApprovalMode = preset
		if err := normalizeLoadedConfig(c); err != nil {
			t.Fatal(err)
		}
	}
}
