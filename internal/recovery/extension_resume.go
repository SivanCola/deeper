package recovery

import "deepzora/internal/extension"

// AssessRuntimeResume returns whether checkpoint/session resume can claim a
// clean rollback for the published (or given) runtime generation.
//
// Policy is owned by extension.DecideResume: irreversible work never yields
// CleanRollback=true; resume itself remains allowed with awareness.
func AssessRuntimeResume(owner *extension.RuntimeOwner, gen uint64) extension.ResumeDecision {
	if owner == nil {
		return extension.ResumeDecision{AllowResume: true, Notes: []string{"runtime owner unavailable"}}
	}
	if gen == 0 {
		gen = owner.Gate.Published()
	}
	return owner.DecideResume(gen)
}

// ResumeNotesForGeneration formats doctor-facing notes for a generation.
func ResumeNotesForGeneration(owner *extension.RuntimeOwner, gen uint64) []string {
	d := AssessRuntimeResume(owner, gen)
	out := make([]string, 0, len(d.Notes)+2)
	if d.HasIrreversible {
		out = append(out, "irreversible external effects present; clean rollback is not claimed")
	}
	if !d.CleanRollback {
		out = append(out, "resume allowed with awareness; compensation incomplete or not applicable")
	}
	out = append(out, d.Notes...)
	return out
}
