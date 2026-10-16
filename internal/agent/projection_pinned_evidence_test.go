package agent

import (
	"testing"

	"deepzora/internal/provider"
)

func TestProjectionRequiresPinnedContextEvidence(t *testing.T) {
	canonical := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "<pinned_context_revision>private pinned body</pinned_context_revision>"},
		{Role: provider.RoleUser, Content: "visible question"},
	}
	projection := CompactionState{
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleUser, Content: "summary"}},
			CoveredCount:      len(canonical),
			CoveredPrefixHash: coveredPrefixHash(canonical, len(canonical)),
		},
	}
	if projectionContentValid(projection, canonical) {
		t.Fatal("projection accepted pinned context without provenance evidence")
	}

	withoutRevision := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "visible question"},
	}
	projection.Projection.CoveredCount = len(withoutRevision)
	projection.Projection.CoveredPrefixHash = coveredPrefixHash(withoutRevision, len(withoutRevision))
	if !projectionContentValid(projection, withoutRevision) {
		t.Fatal("projection without pinned context required nonexistent provenance")
	}
}
