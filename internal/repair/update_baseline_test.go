package repair

import (
	"testing"
)

func TestBaselineRejectsFlatFileUpdateTransactions(t *testing.T) {
	tx := &UpdateTransaction{SchemaVersion: updateTransactionVersion, TargetKind: "file", ToVersion: "v2", Platform: "windows/amd64", CreatedAt: "2026-01-01T00:00:00Z", TargetPath: "/old/deepzora-desktop.exe", BackupPath: "/old/backup"}
	if err := validateUpdateTransactionForLauncher(tx, "/old/deepzora-guard.exe"); err == nil {
		t.Fatal("accepted retired flat updater transaction")
	}
}
