package control

import (
	"log/slog"
	"slices"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/turnevent"
)

func (c *Controller) updateTurnLedgerTranscript(ledger *turnevent.Ledger) *provider.ReadCompletion {
	if c.executor != nil && c.executor.Session() != nil {
		session := c.executor.Session()
		messages, rewrite := session.DisplayBaseline()
		ledger.SetTranscriptRewriteEpoch(rewrite)
		digest, digestErr := session.ContentDigest()
		if digestErr != nil {
			slog.Warn("controller: compute terminal transcript digest", "err", digestErr)
		} else {
			ledger.SetTranscriptSnapshot(int64(session.TranscriptVersion()), digest)
		}
		for _, message := range slices.Backward(messages) {
			if message.ReadCompletion != nil {
				return message.ReadCompletion
			}
			if agent.IsUserAuthoredTurnMessage(message) {
				break
			}
		}
	}
	return nil
}
