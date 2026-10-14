package recovery

import (
	"testing"

	"deepzora/internal/extension"
)

func TestAssessRuntimeResumeIrreversible(t *testing.T) {
	owner := extension.NewRuntimeOwner()
	owner.Gate.Publish(99)
	owner.RecordMessageSent(99, "t1", "test")
	d := AssessRuntimeResume(owner, 0)
	if d.CleanRollback {
		t.Fatal("expected unclean rollback")
	}
	if !d.AllowResume {
		t.Fatal("expected allow resume")
	}
	notes := ResumeNotesForGeneration(owner, 99)
	if len(notes) == 0 {
		t.Fatal("expected notes")
	}
}
