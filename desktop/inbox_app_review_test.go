package main

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/sessioninbox"
)

func TestInboxWailsErrorsUseStableCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "paused", err: sessioninbox.ErrPaused, code: "inbox_paused"},
		{name: "item capacity", err: sessioninbox.ErrCapacityItems, code: "inbox_capacity_items"},
		{name: "byte capacity", err: sessioninbox.ErrCapacityBytes, code: "inbox_capacity_bytes"},
		{name: "item too large", err: sessioninbox.ErrItemTooLarge, code: "inbox_item_too_large"},
		{name: "not found", err: sessioninbox.ErrNotFound, code: "inbox_item_not_found"},
		{name: "invalid state", err: sessioninbox.ErrInvalidState, code: "inbox_invalid_state"},
		{name: "newer schema", err: sessioninbox.ErrSchemaReadonly, code: "inbox_schema_readonly"},
		{name: "closed", err: sessioninbox.ErrClosed, code: "inbox_closed"},
		{name: "empty", err: sessioninbox.ErrEmpty, code: "inbox_empty"},
		{name: "idempotency conflict", err: sessioninbox.ErrIdempotencyConflict, code: "inbox_idempotency_conflict"},
		{name: "wrapped sentinel", err: fmt.Errorf("steer: %w", sessioninbox.ErrPaused), code: "inbox_paused"},
		{name: "read only channel", err: errors.New("channel session is read-only"), code: "channel_read_only"},
		{name: "workspace starting", err: errors.New("workspace is still starting"), code: "workspace_starting"},
		{name: "workspace start failed", err: errors.New("workspace failed to start: internal detail"), code: "workspace_start_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inboxBridgeError(tt.err)
			if got.Error() != "deepzora_error:"+tt.code {
				t.Fatalf("error = %q, want stable code %q", got, tt.code)
			}
			if !errors.Is(got, tt.err) {
				t.Fatalf("wrapped error no longer preserves errors.Is for %v", tt.err)
			}
		})
	}

	unknown := errors.New("filesystem detail: /private/example")
	//nolint:errorlint // Identity is the contract: unknown diagnostics must not be wrapped.
	if got := inboxBridgeError(unknown); got != unknown {
		t.Fatalf("unknown diagnostic error = %q, want original error", got)
	}
}

func TestSteerInboxItemForTurnPausedReturnsStableCode(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	app, ctrl, path := currentInboxTestApp(t, control.Options{Runner: runner, Sink: event.Discard})
	cleanupExactTurnController(t, ctrl)
	ctrl.Submit("active turn")
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	turnID := ctrl.PublishedRuntimeStateSnapshot().TurnID
	if err := ctrl.SetInboxPaused(true); err != nil {
		t.Fatal(err)
	}
	target, err := app.CaptureInboxTarget("test", path)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := app.EnqueueInboxFollowupForTarget(target, "guide the turn", "guide the turn", nil, "paused-steer")
	if err != nil {
		t.Fatal(err)
	}
	failedReceipt, err := app.SteerInboxItemForTurn("test", turnID, receipt.ItemID)
	if err == nil || err.Error() != "deepzora_error:inbox_paused" {
		t.Fatalf("SteerInboxItemForTurn error = %v, want stable paused code", err)
	}
	if failedReceipt.Error != "deepzora_error:inbox_paused" {
		t.Fatalf("SteerInboxItemForTurn receipt error = %q, want stable paused code", failedReceipt.Error)
	}
}

func TestEnqueueInboxSteerWhenPausedQueuesFollowup(t *testing.T) {
	app, ctrl, path := currentInboxTestApp(t, control.Options{Sink: event.Discard})
	t.Cleanup(ctrl.Close)
	if err := ctrl.SetInboxPaused(true); err != nil {
		t.Fatal(err)
	}
	target, err := app.CaptureInboxTarget("test", path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.InboxQueueForTarget(target, control.InboxQueueRequest{Kind: "enqueue_steer", TurnID: "finished", Display: "guide later", Text: "guide later", IdempotencyKey: "paused-enqueue-steer"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt == nil {
		t.Fatal("enqueue steer returned no durable receipt")
	}
	if result.Receipt.Disposition != sessioninbox.DispositionQueuedFollowup || !result.Receipt.Paused {
		t.Fatalf("enqueue steer receipt = %+v, want queued_followup while paused", result.Receipt)
	}
}

func TestDurableInvocationFollowupPreservesEmptyExplicitTask(t *testing.T) {
	app, ctrl, path := currentInboxTestApp(t, control.Options{Sink: event.Discard})
	t.Cleanup(ctrl.Close)
	if err := ctrl.SetInboxPaused(true); err != nil {
		t.Fatal(err)
	}
	target, err := app.CaptureInboxTarget("test", path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := app.EnqueueInboxFollowupForTarget(
		target, "/init", "", []InvocationRequest{{Name: "init", Kind: "skill"}}, "desktop-submit-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, env, err := ctrl.ReadInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if env.SubmitText != "" || env.RawText != "" {
		t.Fatalf("entity-only durable task degraded to text: submit=%q raw=%q", env.SubmitText, env.RawText)
	}
	if len(env.Invocations) != 1 || env.Invocations[0].Name != "init" || env.Invocations[0].Kind != "skill" {
		t.Fatalf("durable invocation metadata = %+v", env.Invocations)
	}
}
