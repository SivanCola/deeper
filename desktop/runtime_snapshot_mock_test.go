package main

import "deepzora/internal/event"

// Test controllers initialize the same snapshot schema as production owners.
func runtimeSnapshotForTest(state event.RuntimeStateSnapshot) event.RuntimeStateSnapshot {
	state.SchemaVersion = 1
	if state.Phase == "" {
		state.Phase = "idle"
		if state.Running {
			state.Phase = "executing"
		}
	}
	return state
}
