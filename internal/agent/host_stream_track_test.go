package agent

import (
	"context"
	"testing"

	"deepzora/internal/extension"
)

func TestTrackPublishedHostStreamUsesContextOwner(t *testing.T) {
	first := extension.NewRuntimeOwner()
	second := extension.NewRuntimeOwner()
	first.Gate.Publish(1)
	second.Gate.Publish(2)

	streamCtx, cancel := context.WithCancel(context.Background())
	untrack := trackPublishedHostStream(extension.ContextWithRuntimeOwner(streamCtx, first), cancel)
	defer untrack()

	second.Gate.ForceExpireDrain(2)
	select {
	case <-streamCtx.Done():
		t.Fatal("sibling owner cancelled this runtime's host stream")
	default:
	}
	first.Gate.ForceExpireDrain(1)
	select {
	case <-streamCtx.Done():
	default:
		t.Fatal("owning runtime did not cancel its host stream")
	}
}

func TestUnboundHostStreamDoesNotJoinPublishedRuntime(t *testing.T) {
	owner := extension.NewRuntimeOwner()
	owner.Gate.Publish(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	untrack := trackPublishedHostStream(ctx, cancel)
	defer untrack()
	owner.Gate.ForceExpireDrain(1)
	select {
	case <-ctx.Done():
		t.Fatal("unbound stream was cancelled by another runtime")
	default:
	}
}
