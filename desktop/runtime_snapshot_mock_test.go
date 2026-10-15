package main

import (
	"deepzora/internal/control"
	"deepzora/internal/event"
)

// Status-backed test controllers explicitly publish the current snapshot contract.
func runtimeSnapshotForTest(status control.RuntimeStatus) event.RuntimeStateSnapshot {
	phase := "idle"
	if status.Running {
		phase = "executing"
	}
	return event.RuntimeStateSnapshot{SchemaVersion: 1, Phase: phase, Running: status.Running, PendingPrompt: status.PendingPrompt,
		BackgroundJobs: status.BackgroundJobs, Cancellable: status.Cancellable, CancelRequested: status.CancelRequested,
		TurnID: status.TurnID, TurnStatus: status.Status, TurnEventSeq: status.TurnEventSeq}
}
