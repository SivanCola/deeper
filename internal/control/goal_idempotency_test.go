package control

import (
	"testing"
)

func TestGoalSetIsIdempotent(t *testing.T) {
	g := &goalMachine{}
	if ok := g.set("same goal"); !ok {
		t.Fatal("initial set did not change")
	}
	if ok := g.set("same goal"); ok {
		t.Fatal("same running goal was not idempotent")
	}
	if ok := g.set("new goal"); !ok {
		t.Fatal("new goal did not change")
	}
}
