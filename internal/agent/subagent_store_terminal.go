package agent

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (s *SubagentStore) SaveCompleted(run *SubagentRun) error {
	return s.SaveOutcome(run, SubagentOutcome{Ref: runRef(run), Status: SubagentOutcomeCompleted})
}

func (s *SubagentStore) SaveFailed(run *SubagentRun) error {
	return s.SaveOutcome(run, SubagentOutcome{Ref: runRef(run), Status: SubagentOutcomeFailed})
}

func (s *SubagentStore) SaveOutcome(run *SubagentRun, outcome SubagentOutcome) error {
	if s == nil || run == nil || run.Ref == "" || s.parentDestroyed(run) {
		return nil
	}
	if run.terminalPersisted {
		if terminalOutcomeAlreadyRecorded(run.Meta, outcome) {
			return nil
		}
		return fmt.Errorf("subagent %q already has a persisted terminal outcome", run.Ref)
	}
	var sessionErr error
	if run.Persistence != nil {
		sessionErr = run.Persistence.Flush(context.Background())
	}
	meta := run.Meta
	switch outcome.Status {
	case SubagentOutcomeCompleted:
		meta.Status = SubagentCompleted
	case SubagentOutcomeCancelled:
		meta.Status = SubagentInterrupted
	default:
		meta.Status = SubagentFailed
	}
	meta.Outcome = string(outcome.Status)
	meta.Retryable = outcome.Retryable
	meta.ErrorCode = outcome.ErrorCode
	meta.UpdatedAt = time.Now().UTC()
	run.Meta = meta
	err := errors.Join(sessionErr, s.saveMeta(meta))
	if err == nil {
		run.terminalPersisted = true
	}
	return err
}

func terminalOutcomeAlreadyRecorded(meta SubagentMeta, outcome SubagentOutcome) bool {
	if meta.Outcome == "" {
		return false
	}
	if meta.Outcome != string(outcome.Status) || meta.Retryable != outcome.Retryable || meta.ErrorCode != outcome.ErrorCode {
		return false
	}
	switch outcome.Status {
	case SubagentOutcomeCompleted:
		return meta.Status == SubagentCompleted
	case SubagentOutcomeCancelled:
		return meta.Status == SubagentInterrupted
	case SubagentOutcomePartial, SubagentOutcomeFailed:
		return meta.Status == SubagentFailed
	default:
		return false
	}
}

func runRef(run *SubagentRun) string {
	if run == nil {
		return ""
	}
	return run.Ref
}
