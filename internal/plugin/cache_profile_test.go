package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProfileCacheIsolation(t *testing.T) {
	redirectCache(t)
	spec := sampleSpec()
	profiles := []HostProfile{HostProfileCore, HostProfileInteractive, HostProfileDesktopApps}
	for _, profile := range profiles {
		cs := sampleCachedSchema(SchemaCacheKey(spec))
		cs.Tools[0].Name = profile.String()
		if err := SaveCachedSchemaForProfile(profile, spec.Name, cs); err != nil {
			t.Fatal(err)
		}
	}
	for _, profile := range profiles {
		cs, ok := LoadCachedSchemaForSpecProfile(spec, profile)
		if !ok || cs.Version != cacheVersion || cs.Profile != profile.String() || cs.Tools[0].Name != profile.String() {
			t.Fatalf("profile %s: %+v %v", profile, cs, ok)
		}
	}
}

// TestEnhancedCacheFutureVersionIsMiss ensures a newer layout is a miss, not a
// crash or a silent reuse.
func TestEnhancedCacheFutureVersionIsMiss(t *testing.T) {
	redirectCache(t)
	spec := sampleSpec()
	path := cachePathForProfile(spec.Name, HostProfileDesktopApps)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	future := CachedSchema{
		Version:      enhancedCacheVersion + 1,
		Profile:      HostProfileDesktopApps.String(),
		CacheKey:     SchemaCacheKey(spec),
		Capabilities: map[string]bool{"tools": true},
		Tools:        []CachedTool{{Name: "future", Schema: []byte(`{"type":"object"}`)}},
	}
	b, err := json.Marshal(future)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadCachedSchemaForSpecProfile(spec, HostProfileDesktopApps); ok {
		t.Fatal("future cache version must be a miss")
	}
}

// TestEnhancedCacheLegacyToolVisibility pins the default visibility rule:
// a tool with no visibility metadata stays model-visible.
func TestEnhancedCacheLegacyToolVisibility(t *testing.T) {
	redirectCache(t)
	tool := CachedTool{Name: "plain", Schema: []byte(`{"type":"object"}`)}
	if !tool.ToolIsModelVisible() {
		t.Fatal("absent visibility must default to model-visible")
	}
	appOnly := CachedTool{Name: "app-only", Visibility: []string{"app"}}
	if appOnly.ToolIsModelVisible() {
		t.Fatal("app-only visibility must be hidden from the model catalog")
	}
}

// TestProfileHashStableAndDistinct guards the cache filename identity.
func TestProfileHashStableAndDistinct(t *testing.T) {
	interactive := HostProfileInteractive.ProfileCacheHash()
	desktop := HostProfileDesktopApps.ProfileCacheHash()
	core := HostProfileCore.ProfileCacheHash()
	if interactive == desktop || desktop == core || interactive == core {
		t.Fatalf("profile hashes collided: %s %s %s", core, interactive, desktop)
	}
	if HostProfileInteractive.ProfileCacheHash() != interactive {
		t.Fatal("profile hash not stable")
	}
	if len(interactive) != 12 {
		t.Fatalf("hash length = %d, want 12", len(interactive))
	}
}

// TestSaveEnhancedSetsTimestamp ensures LastValidated is stamped exactly once.
func TestSaveEnhancedSetsTimestamp(t *testing.T) {
	redirectCache(t)
	spec := sampleSpec()
	before := time.Now().UTC().Add(-time.Second)
	if err := SaveCachedSchemaForProfile(HostProfileInteractive, spec.Name, CachedSchema{
		CacheKey:     SchemaCacheKey(spec),
		Capabilities: map[string]bool{"tools": true},
	}); err != nil {
		t.Fatal(err)
	}
	cs, ok := LoadCachedSchemaForSpecProfile(spec, HostProfileInteractive)
	if !ok {
		t.Fatal("load after save failed")
	}
	if cs.LastValidated.Before(before) {
		t.Fatalf("LastValidated = %v, want >= %v", cs.LastValidated, before)
	}
}
