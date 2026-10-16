package control

import (
	"crypto/rand"
	"deepzora/internal/provider"
	"deepzora/internal/transcript"
	"deepzora/internal/turnevent"
)

// Ephemeral controllers project resident messages without disk state.
// Persistent controllers use the host runtime's event-derived projection.
func (c *Controller) newMemoryTranscriptProjection(ledger *turnevent.Ledger) (*transcript.Projection, error) {
	identity := transcript.Identity{RuntimeEpoch: rand.Text()}
	ledger.SetRuntimeEpoch(identity.RuntimeEpoch)
	var messages []provider.Message
	if c.executor != nil && c.executor.Session() != nil {
		messages = c.executor.Session().Snapshot()
	}
	rows := transcript.History(messages, transcript.HistoryOptions{CheckpointTurns: c.CheckpointTurnsByMessageIndex(), SubmitContent: func(m provider.Message) string {
		return StripReferencedContextPrefix(StripComposePrefixes(m.Content))
	}, UserContent: func(m provider.Message) string {
		if m.RawContent != "" {
			return m.RawContent
		}
		return StripReferencedContextPrefix(StripComposePrefixes(m.Content))
	}})
	return transcript.NewProjection(identity, rows, 0)
}
