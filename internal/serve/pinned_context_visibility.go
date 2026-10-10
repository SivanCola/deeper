package serve

import (
	"deeper/internal/agent"
	"deeper/internal/provider"
)

func historyWithoutPinnedContextRevisions(messages []provider.Message) []provider.Message {
	out := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		if !agent.IsPinnedContextRevision(message) {
			out = append(out, message)
		}
	}
	return out
}
