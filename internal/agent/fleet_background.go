package agent

import (
	"context"
	"deeper/internal/checkpoint"
	"deeper/internal/event"
)

func fleetCallContext(ctx context.Context) (string, event.Sink) {
	id, sink, _, ok := CallContext(ctx)
	if !ok || sink == nil {
		return "fleet", event.Discard
	}
	return id, sink
}

func rejectedFleetStart(observer *checkpoint.MutationObserver, writerID string, registered bool, err error) (string, error) {
	if registered {
		observer.UnregisterWriter(writerID)
	}
	return "", err
}
