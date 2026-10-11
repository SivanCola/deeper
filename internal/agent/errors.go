package agent

import (
	"errors"
	"strings"

	"deepzora/internal/provider"
)

// ReasoningReplayFailure classifies why an assistant turn could not safely be
// committed to provider-visible history.
type ReasoningReplayFailure string

const (
	ReasoningReplayMissing      ReasoningReplayFailure = "missing_required_reasoning"
	ReasoningReplayOverflow     ReasoningReplayFailure = "reasoning_overflow"
	ReasoningReplayIncomplete   ReasoningReplayFailure = "incomplete_reasoning"
	ReasoningReplayUnreplayable ReasoningReplayFailure = "unreplayable_history"
)

// ReasoningReplayError stops client tools before execution when their provider
// reasoning cannot be replayed. Completed work is retained as LocalOnly by the
// ordinary interrupted-turn recovery path.
type ReasoningReplayError struct {
	Kind ReasoningReplayFailure
}

func (e *ReasoningReplayError) Error() string {
	if e != nil && e.Kind == ReasoningReplayIncomplete {
		return "The provider ended the response with unfinished reasoning. Deepzora kept existing work and did not run the requested tools; retry to continue safely."
	}
	if e != nil && e.Kind == ReasoningReplayOverflow {
		return "The provider reasoning exceeded the client safety limit, so Deepzora did not run the requested tools. Existing work was kept; retry to continue safely."
	}
	return "The provider repeatedly omitted reasoning required to replay this tool turn. Deepzora exhausted its safe automatic recovery and did not run the requested tools. Existing work was kept; switch provider or protocol if this continues."
}

// PauseClass names the guard that deliberately ended a run, so a host can
// classify an outcome without reaching into the unexported pause types.
// Empty for ordinary provider/tool failures.
func PauseClass(err error) string {
	var budgetPause *taskBudgetPause
	if errors.As(err, &budgetPause) {
		return "task_budget"
	}
	var maxSteps *maxStepsPause
	if errors.As(err, &maxSteps) {
		return "max_steps"
	}

	var incompleteRead *IncompleteReadError
	if errors.As(err, &incompleteRead) {
		return "incomplete_read"
	}
	return ""
}

// IncompleteReadError is a recoverable run boundary: a read_file result was
// only partially visible and the host refused to let the model silently treat
// it as complete. It carries only routing/size metadata, never file contents.
type IncompleteReadError struct {
	Pause         *provider.ReadPause
	Reason        string
	Path          string
	ToolCallID    string
	ResultRef     string
	NextOffset    int
	ConsumedBytes int
	TotalBytes    int
}

func (e *IncompleteReadError) Error() string {
	if e == nil {
		return "read_file did not complete"
	}
	detail := strings.TrimSpace(e.Reason)
	if detail == "" {
		detail = "the retained result still has unread content"
	}
	return "read_file did not complete safely: " + detail
}

// RunPauseInfo is the stable host-facing description of a deliberate Run
// boundary. It keeps unexported control-flow error types private while allowing
// Controller to distinguish task budgets from an explicit runtime max_steps.
type RunPauseInfo struct {
	Kind      string
	Limit     int
	Key       string
	HostOwned bool
	Reason    string
}

// InspectRunPause unwraps a deliberate explicit run boundary.
func InspectRunPause(err error) (RunPauseInfo, bool) {
	var maxSteps *maxStepsPause
	if errors.As(err, &maxSteps) {
		return RunPauseInfo{Kind: "max_steps", Limit: maxSteps.steps, Key: maxSteps.key}, true
	}
	var budget *taskBudgetPause
	if errors.As(err, &budget) {
		return RunPauseInfo{Kind: "task_budget", Key: budget.axis, HostOwned: true, Reason: budget.detail}, true
	}
	var incompleteRead *IncompleteReadError
	if errors.As(err, &incompleteRead) {
		return RunPauseInfo{Kind: "incomplete_read", HostOwned: true, Reason: incompleteRead.Reason}, true
	}
	return RunPauseInfo{}, false
}
