package agent

import (
	"path/filepath"
	"testing"
)

// Both caller IDs and IDs read from current immutable headers must be resolved
// by the catalog before a lineage walk can authorize a child transcript.
func TestSessionLineageRejectsTraversalIdentifiers(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))

	for _, id := range []string{"../evil", "..", ".", "nested/evil", "/abs"} {
		if _, err := store.sessionAncestors(id); err == nil {
			t.Fatalf("sessionAncestors(%q) error = %v, want invalid-identifier rejection", id, err)
		}
		if _, err := store.isAncestorSession("root", id); err == nil {
			t.Fatalf("isAncestorSession(root, %q) error = %v, want invalid-identifier rejection", id, err)
		}
	}
}

func TestSessionLineageRejectsTraversalParentFromMetadata(t *testing.T) {
	sessionDir := t.TempDir()
	store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))

	// A well-formed child whose stored metadata declares a traversal parent:
	// the walk must stop with a validation error instead of joining the path.
	saveTestParentSession(t, sessionDir, "child", "")
	rewriteTestParentHeader(t, sessionDir, "child", "parentSessionId", "../evil")
	if _, err := store.sessionAncestors("child"); err == nil {
		t.Fatalf("sessionAncestors(child) error = %v, want invalid-identifier rejection for stored parent", err)
	}
}
