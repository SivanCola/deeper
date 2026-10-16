package turnevent

// transcriptSnapshot stamps the current in-memory message revision on runtime events.
type transcriptSnapshot struct {
	revision     int64
	digest       string
	rewriteEpoch uint64
}

func (l *Ledger) SetTranscriptRewriteEpoch(epoch uint64) {
	l.mu.Lock()
	l.transcript.rewriteEpoch = epoch
	l.mu.Unlock()
}

func (l *Ledger) SetTranscriptSnapshot(revision int64, digest string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.transcript.revision, l.transcript.digest = revision, digest
	l.mu.Unlock()
}

// terminalSummaryFor folds a terminal envelope into the summary a checkpoint
// keeps once its events are compacted away.
func terminalSummaryFor(rec Envelope, outcome string, started int64) TerminalSummary {
	summary := TerminalSummary{
		TurnID: rec.TurnID, TerminalSequence: rec.Sequence, Status: rec.Status, Outcome: outcome,
		RuntimeEpoch: rec.RuntimeEpoch, SubmissionID: rec.SubmissionID,
		StartedAt: started, FinishedAt: rec.CreatedAt,
		TranscriptRevision: rec.TranscriptRevision, TranscriptDigest: rec.TranscriptDigest,
	}
	if started > 0 && rec.CreatedAt >= started {
		summary.DurationMs = rec.CreatedAt - started
	}
	return summary
}
