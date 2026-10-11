package agent

import (
	"context"
	"crypto/sha256"
	"deepzora/internal/provider"
	"deepzora/internal/store"
	"encoding/json"
	"os"
	"time"
)

// appendDAGFixture simulates a second writer committing a current DAG batch
// before refreshing derived files or the revision ledger.
func appendDAGFixture(path string, msgs []provider.Message, digest [sha256.Size]byte, revision int64, reason string) error {
	branch, err := loadSessionUnlocked(path)
	if err != nil {
		return err
	}
	state, err := replaySessionDAG(context.Background(), SessionEventLogPath(path), defaultSessionReplayLimits)
	if err != nil {
		return err
	}
	branch.Rewrite(msgs, reason)
	branch.ensureMessageIDsForSave(msgs)
	plan, err := branch.planDAGWrite(path, state, msgs, sessionSaveRewrite, time.Now().UTC())
	if err != nil {
		return err
	}
	_, err = appendSessionDAGEntries(path, plan.entries, true)
	return err
}

func writeLeaseOwnerFixture(path string, info SessionLeaseInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(store.SessionLeaseLock(path), sessionLeaseOwnerBytes(data), 0600)
}
