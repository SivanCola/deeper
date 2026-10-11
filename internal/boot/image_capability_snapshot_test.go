package boot

import (
	"context"
	"deepzora/internal/event"
	"testing"
)

func TestBuildImageCapabilityFrozenUntilRebuild(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	base := `default_model = "relay/x"
[agent]
system_prompt = "stable prefix"
[[providers]]
name = "relay"
kind = "openai"
base_url = "http://localhost:1"
models = ["x"]
`
	writeFile(t, dir, "deepzora.toml", base)
	approveWorkspace(t, dir)
	old, err := Build(context.Background(), Options{Sink: event.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if !old.ImageInputEnabled() || old.ImageCapabilityChanged() {
		t.Fatal("an undeclared model must pass images to its provider and the snapshot must be current")
	}
	writeFile(t, dir, "deepzora.toml", base+"[providers.model_overrides.x]\nvision = false\n")
	approveWorkspace(t, dir)
	if !old.ImageInputEnabled() || !old.ImageCapabilityChanged() {
		t.Fatal("saved setting must invalidate, not mutate old runtime")
	}
	next, err := Build(context.Background(), Options{Sink: event.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.ImageInputEnabled() || next.ImageCapabilityChanged() {
		t.Fatal("a model declared text-only must be blocked after rebuild")
	}
	writeFile(t, dir, "deepzora.toml", base+"[providers.model_overrides.x]\nvision = true\n")
	approveWorkspace(t, dir)
	if next.ImageInputEnabled() || !next.ImageCapabilityChanged() {
		t.Fatal("running snapshot switched before rebuild")
	}
}
