package control

import (
	"path/filepath"
	"testing"
)

func TestGoalSetIsIdempotent(t *testing.T) {
	g := &goalMachine{statePath: filepath.Join(t.TempDir(), "goal.json")}
	if _, _, ok := g.set("same goal"); !ok {
		t.Fatal("initial set did not persist")
	}
	if _, _, ok := g.set("same goal"); ok {
		t.Fatal("same running goal was not idempotent")
	}
	if _, _, ok := g.set("new goal"); !ok {
		t.Fatal("new goal did not persist")
	}
}
