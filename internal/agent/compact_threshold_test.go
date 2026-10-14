package agent

import "testing"

// An output budget larger than the window it shares must never change the
// sole compact_ratio trigger. Output is clipped only at send time.
func TestCompactTriggerIndependentOfOutputBudget(t *testing.T) {
	a := &Agent{
		svc:         agentServices{prov: &sharedWindowTestProvider{budget: 131_072, shared: true}},
		agentConfig: agentConfig{contextWindow: 128_000, compactRatio: defaultCompactRatio},
	}
	trigger := a.compactTrigger()
	want := int(float64(128_000) * defaultCompactRatio)
	if trigger != want {
		t.Fatalf("trigger = %d, want %d (compact_ratio only)", trigger, want)
	}
	// Oversized output budget must not lower the trigger.
	a.svc.prov = &sharedWindowTestProvider{budget: 200_000, shared: true}
	if got := a.compactTrigger(); got != want {
		t.Fatalf("trigger after larger output budget = %d, want %d", got, want)
	}
	hard := a.hardInputCeiling()
	if hard != 128_000-protocolReserveTokens {
		t.Fatalf("hard ceiling = %d, want window-protocolReserve", hard)
	}
}

func TestRecentTailBudgetIsFixedSixteenPercent(t *testing.T) {
	cases := []struct {
		window int
		want   int
	}{
		{10_000, 1_600},
		{128_000, 20_480},
		{400_000, 64_000},
		{1_000_000, 160_000},
	}
	for _, tc := range cases {
		a := &Agent{agentConfig: agentConfig{contextWindow: tc.window, compactRatio: defaultCompactRatio}}
		if got := a.recentTailBudget(); got != tc.want {
			t.Fatalf("window %d: recentTailBudget = %d, want %d", tc.window, got, tc.want)
		}
	}
}

func TestDefaultCompactRatioIsEightyPercent(t *testing.T) {
	if defaultCompactRatio != 0.80 {
		t.Fatalf("defaultCompactRatio = %v, want 0.80", defaultCompactRatio)
	}
}

func TestExplicitLegacyCompactRatioStillApplies(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 1_000_000, compactRatio: 0.85}}
	if got := a.compactTrigger(); got != 850_000 {
		t.Fatalf("compactTrigger = %d, want 850000", got)
	}
}

func TestLowCompactRatioTriggerAtThirtyPercent(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 1_000_000, compactRatio: 0.30}}
	if got := a.compactTrigger(); got != 300_000 {
		t.Fatalf("compactTrigger = %d, want 300000", got)
	}
}

func TestAcceptCheckpointCandidateRules(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 1_000_000, compactRatio: 0.85}}
	// 20% candidate under normal path: accept.
	if err := a.acceptCheckpointCandidate(CompactionTriggerPressure, 850_000, 180_000); err != nil {
		t.Fatalf("20%% candidate: %v", err)
	}
	// Any strictly smaller candidate below the physical ceiling is accepted;
	// pressure convergence may summarize it once more.
	if err := a.acceptCheckpointCandidate(CompactionTriggerPressure, 850_000, 510_000); err != nil {
		t.Fatalf("51%% reducing candidate: %v", err)
	}
	// Large candidates remain valid while they shrink and stay below hard.
	if err := a.acceptCheckpointCandidate(CompactionTriggerPressure, 900_000, 600_000); err != nil {
		t.Fatalf("large reducing candidate: %v", err)
	}
	// Small but real savings are valid too.
	if err := a.acceptCheckpointCandidate(CompactionTriggerPressure, 600_000, 550_000); err != nil {
		t.Fatalf("small reducing candidate: %v", err)
	}
	// Manual below trigger: accept any reduction.
	if err := a.acceptCheckpointCandidate(CompactionTriggerManual, 100_000, 80_000); err != nil {
		t.Fatalf("manual below trigger: %v", err)
	}
}
