package agent

import (
	"context"
	"deepzora/internal/provider"
	"deepzora/internal/secrets"
	"deepzora/internal/store"
	"encoding/json"
	"os"
	"time"
)

// RedactSessionLog masks every current DAG branch and physically erases stale
// records under the same lease and file locks as a live session. The caller
// owns a SessionWriter for path; dry runs never mutate storage.
func RedactSessionLog(path string, dryRun bool) (changed bool, rewritten int64, err error) {
	shell := NewSession("")
	err = shell.withSessionSaveLocks(path, func() error {
		ctx := context.Background()
		st, e := replaySessionDAG(ctx, store.SessionEventLog(path), defaultSessionReplayLimits)
		if e != nil {
			return e
		}
		mask := func(m provider.Message) provider.Message {
			redacted := secrets.RedactMessages([]provider.Message{m})[0]
			before, _ := json.Marshal(m)
			after, _ := json.Marshal(redacted)
			if string(before) != string(after) {
				changed = true
			}
			return redacted
		}
		for id, n := range st.nodes {
			// Original and overlay bytes may contain secrets superseded by a patch.
			mask(n.msg)
			if m, ok := st.patches[id]; ok {
				mask(m)
			}
			st.redactions[id] = mask(st.appliedMessage(n))
		}
		for _, h := range st.heads {
			if h.system != nil {
				m := mask(*h.system)
				h.system = &m
			}
			name := secrets.Redact(h.name)
			if name != h.name {
				changed = true
				h.name = name
			}
		}
		if !changed || dryRun {
			return nil
		}
		now := time.Now().UTC()
		if e := sessionDAGSingleWriterProof(path, st, now); e != nil {
			return e
		}
		if e := rotateSessionDAG(path, st, now); e != nil {
			return e
		}
		fresh, e := replaySessionDAG(ctx, store.SessionEventLog(path), defaultSessionReplayLimits)
		if e != nil {
			return e
		}
		msgs, _ := fresh.materialize(fresh.selectedHead())
		if e := writeSessionMessages(path, msgs); e != nil {
			return e
		}
		if e := writeSessionDAGIndex(ctx, path, fresh); e != nil {
			return e
		}
		for _, artifact := range []string{path, store.SessionEventLog(path)} {
			if info, e := os.Stat(artifact); e == nil {
				rewritten += info.Size()
			}
		}
		return nil
	})
	if err == nil && changed && !dryRun {
		_, err = RepairSessionListingProjection(context.Background(), path)
	}
	return
}
