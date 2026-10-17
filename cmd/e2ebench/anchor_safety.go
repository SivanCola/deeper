package main

import "fmt"

// anchorSafetySummary aggregates the content-free runtime audit. It measures
// the shadow decisions and their reasons. It cannot independently re-read
// source because trajectories intentionally omit
// paths, anchors, and line hashes.
type anchorSafetySummary struct {
	Samples           int            `json:"samples,omitempty"`
	ShadowAllows      int            `json:"shadow_allows,omitempty"`
	NoEligibleReads   int            `json:"no_eligible_reads,omitempty"`
	PartialWindows    int            `json:"partial_windows,omitempty"`
	TargetChanged     int            `json:"target_changed,omitempty"`
	NativeInvalid     int            `json:"native_invalid,omitempty"`
	SameBatchReads    int            `json:"same_batch_reads,omitempty"`
	MaxObservationAge int            `json:"max_observation_age,omitempty"`
	ByTaskMode        map[string]int `json:"by_task_mode,omitempty"`
}

type anchorSafetyRecord struct {
	Mode                  string `json:"mode"`
	TaskMode              string `json:"task_mode"`
	RangeLines            int    `json:"range_lines"`
	ObservationAge        int    `json:"observation_age"`
	ShadowAllowed         bool   `json:"shadow_allowed"`
	Reason                string `json:"reason"`
	SameBatchReadRejected bool   `json:"same_batch_read_rejected"`
}

func (t *trajScan) recordAnchorSafetyAudit(a anchorSafetyRecord) {
	if t.s.AnchorSafety == nil {
		t.s.AnchorSafety = &anchorSafetySummary{ByTaskMode: map[string]int{}}
	}
	s := t.s.AnchorSafety
	s.Samples++
	if a.ShadowAllowed {
		s.ShadowAllows++
	}
	if a.SameBatchReadRejected {
		s.SameBatchReads++
	}
	s.MaxObservationAge = max(s.MaxObservationAge, a.ObservationAge)
	s.ByTaskMode[a.TaskMode]++
	switch a.Reason {
	case "would_block_no_eligible_read":
		s.NoEligibleReads++
	case "would_block_partial_window":
		s.PartialWindows++
	case "would_block_target_changed":
		s.TargetChanged++
	case "native_target_invalid":
		s.NativeInvalid++
	}
}

func renderAnchorSafety(results []result) string {
	var total anchorSafetySummary
	runs := 0
	for _, r := range results {
		if r.Trajectory == nil || r.Trajectory.AnchorSafety == nil || r.Trajectory.AnchorSafety.Samples == 0 {
			continue
		}
		runs++
		a := r.Trajectory.AnchorSafety
		total.Samples += a.Samples
		total.ShadowAllows += a.ShadowAllows
		total.NoEligibleReads += a.NoEligibleReads
		total.PartialWindows += a.PartialWindows
		total.TargetChanged += a.TargetChanged
		total.NativeInvalid += a.NativeInvalid
		total.SameBatchReads += a.SameBatchReads
		total.MaxObservationAge = max(total.MaxObservationAge, a.MaxObservationAge)
		if total.ByTaskMode == nil {
			total.ByTaskMode = map[string]int{}
		}
		for mode, n := range a.ByTaskMode {
			total.ByTaskMode[mode] += n
		}
	}
	if total.Samples == 0 {
		return ""
	}
	line := fmt.Sprintf("**Anchor safety shadow** (%d runs): **samples** %d · **shadow allows** %d · **shadow blocks** %d",
		runs, total.Samples, total.ShadowAllows, total.Samples-total.ShadowAllows)
	line += fmt.Sprintf(" · **same-batch reads** %d · **partial windows** %d · **target changed** %d · **native invalid** %d · **max observation age** %d",
		total.SameBatchReads, total.PartialWindows, total.TargetChanged, total.NativeInvalid, total.MaxObservationAge)
	if len(total.ByTaskMode) > 0 {
		line += fmt.Sprintf(" · **interactive** %d · **loop** %d", total.ByTaskMode["interactive"], total.ByTaskMode["loop"])
	}
	return line + "\n\n"
}
