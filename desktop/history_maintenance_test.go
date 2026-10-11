package main

import (
	"encoding/json"
	"strings"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestMaintenanceHistoryKeepsProgressPendingUntilRuntimeSync(t *testing.T) {
	op := event.SessionOperationInfo{
		OperationID: "maintenance-history", OperationRevision: 3, RuntimeEpoch: "runtime-old",
		Kind: "compact", Activity: "running", Status: "running", InputTokens: 1200,
	}
	payload, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	rows := historyMessages([]provider.Message{{
		ID: "maintenance:maintenance-history", Role: provider.Role("compaction"), Content: string(payload),
	}}, strings.TrimSpace)
	assertPendingMaintenanceHistory(t, rows)
}

func assertPendingMaintenanceHistory(t *testing.T, rows []HistoryMessage) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("history rows = %+v", rows)
	}
	row := rows[0]
	if !row.Pending || row.OperationStatus != "running" || row.OperationID != "maintenance-history" ||
		row.OperationRevision != 3 || row.RuntimeEpoch != "runtime-old" || row.InputTokens != 1200 {
		t.Fatalf("maintenance history row = %+v", row)
	}
}
