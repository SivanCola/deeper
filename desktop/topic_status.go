package main

import "deepzora/internal/event"

// topicStatusAwaitingDelivery is a delivery-check pause, not a recovery pause.
const topicStatusAwaitingDelivery = "awaiting_delivery"

func topicStatusFromTurnDone(outcome string) (string, bool) {
	switch outcome {
	case event.TurnOutcomeIncompleteRead:
		return topicStatusPaused, true
	default:
		return "", false
	}
}
