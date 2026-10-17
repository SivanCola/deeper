package main

import (
	"strings"
	"testing"
)

func TestSummarizeTrajectoryAnchorSafetyAudits(t *testing.T) {
	path := writeTrajectory(t, "anchor-safety.trajectory.jsonl", []string{
		`{"seq":1,"ts":1000,"anchor_safety_audit":{"mode":"shadow","task_mode":"interactive","range_lines":3,"observation_age":2,"shadow_allowed":true,"reason":"would_allow_exact_match"}}`,
		`{"seq":2,"ts":2000,"anchor_safety_audit":{"mode":"shadow","task_mode":"interactive","range_lines":4,"observation_age":0,"shadow_allowed":false,"reason":"same_batch_read_rejected","same_batch_read_rejected":true}}`,
		`{"seq":3,"ts":3000,"anchor_safety_audit":{"mode":"shadow","task_mode":"loop","range_lines":2,"observation_age":5,"shadow_allowed":false,"reason":"would_block_target_changed"}}`,
		`{"seq":4,"ts":4000,"anchor_safety_audit":{"mode":"shadow","task_mode":"loop","reason":"native_target_invalid"}}`,
	})
	s, err := summarizeTrajectory(path)
	if err != nil {
		t.Fatalf("summarizeTrajectory: %v", err)
	}
	a := s.AnchorSafety
	if a == nil || a.Samples != 4 || a.ShadowAllows != 1 || a.SameBatchReads != 1 || a.TargetChanged != 1 || a.NativeInvalid != 1 {
		t.Fatalf("anchor safety summary = %+v", a)
	}
	if a.MaxObservationAge != 5 {
		t.Fatalf("anchor safety observation age = %+v", a)
	}
	if a.ByTaskMode["interactive"] != 2 || a.ByTaskMode["loop"] != 2 {
		t.Fatalf("anchor safety task modes = %+v", a.ByTaskMode)
	}
	got := renderAnchorSafety([]result{{Trajectory: s}})
	for _, want := range []string{"**Anchor safety shadow**", "**samples** 4", "**shadow allows** 1", "**shadow blocks** 3", "**same-batch reads** 1", "**interactive** 2", "**loop** 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("anchor safety report missing %q:\n%s", want, got)
		}
	}
}

func TestSummarizeTrajectoryOmitsAnchorSafetyWithoutSamples(t *testing.T) {
	path := writeTrajectory(t, "no-anchor-safety.trajectory.jsonl", []string{
		`{"seq":1,"ts":1000,"event":{"kind":"turn_started"}}`,
	})
	s, err := summarizeTrajectory(path)
	if err != nil {
		t.Fatalf("summarizeTrajectory: %v", err)
	}
	if s.AnchorSafety != nil || renderAnchorSafety([]result{{Trajectory: s}}) != "" {
		t.Fatalf("trajectory without audit samples reported anchor safety: %+v", s.AnchorSafety)
	}
}
