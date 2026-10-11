package bot

import (
	"testing"
)

// schemaOneTempDir pins a test to the schema-1 session writer: it exercises
// recovery-copy mechanics that only that path has.
func schemaOneTempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}
