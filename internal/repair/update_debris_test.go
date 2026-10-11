package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePendingUpdateRaw drops arbitrary bytes where the transaction lives.
func writePendingUpdateRaw(t *testing.T, body string) string {
	t.Helper()
	path := PendingUpdatePath()
	if path == "" {
		t.Fatal("pending update path unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func quarantinedCopies(t *testing.T) []string {
	t.Helper()
	path := PendingUpdatePath()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	prefix := filepath.Base(path) + ".unusable-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			out = append(out, entry.Name())
		}
	}
	return out
}

// Reconciliation must clear debris too, otherwise startup keeps reporting a
// recovery failure that nothing can resolve.
func TestReconcilePendingUpdateQuarantinesDebris(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	writePendingUpdateRaw(t, `{"schema_version":1,"to_version":"v2"`)

	result, err := ReconcilePendingUpdate("v1")
	if err != nil {
		t.Fatalf("ReconcilePendingUpdate over debris: %v", err)
	}
	if !result.Cleared {
		t.Fatalf("result = %+v, want the debris reported as cleared", result)
	}
	if _, err := os.Lstat(PendingUpdatePath()); !os.IsNotExist(err) {
		t.Fatalf("pending marker still present after reconciliation: %v", err)
	}
	if got := quarantinedCopies(t); len(got) != 1 {
		t.Fatalf("quarantined copies = %v, want the debris preserved for diagnosis", got)
	}
}

// Quarantine preserves evidence rather than deleting it, and repeated recovery
// never overwrites an earlier copy.
func TestQuarantinePendingUpdatePreservesEveryCopy(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	for i := range 3 {
		writePendingUpdateRaw(t, `{"broken":`)
		aside, err := quarantinePendingUpdate("test")
		if err != nil {
			t.Fatalf("quarantine %d: %v", i, err)
		}
		body, err := os.ReadFile(aside)
		if err != nil {
			t.Fatalf("read quarantined copy %d: %v", i, err)
		}
		if string(body) != `{"broken":` {
			t.Fatalf("quarantined body = %q, want the original bytes", body)
		}
	}
	if got := quarantinedCopies(t); len(got) != 3 {
		t.Fatalf("quarantined copies = %v, want all three preserved", got)
	}
}
