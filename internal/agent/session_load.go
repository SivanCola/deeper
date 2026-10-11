package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/store"
)

// sessionLoadResult is one transcript read from disk: the messages of the
// selected head, per-message times, and the current DAG position.
type sessionLoadResult struct {
	msgs       []provider.Message
	times      []time.Time
	fromEvents bool
	damaged    bool
	dag        bool
	head       HeadRef
	headCount  int
	state      *sessionDAGState
	openTurn   *sessionDAGTurn
	events     []HeadEvent
}

// loadSessionMessages reads the current DAG event log. damaged reports a torn
// tail or corrupt record; a current writer can compact that log to heal it.
// Unsupported formats are rejected without checkpoint fallback.
func loadSessionMessages(sessionPath string) (msgs []provider.Message, fromEvents, damaged bool, err error) {
	return loadSessionMessagesWithLimits(sessionPath, defaultSessionReplayLimits, nil)
}

func loadSessionMessagesWithLimits(sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher) (msgs []provider.Message, fromEvents, damaged bool, err error) {
	return loadSessionMessagesWithContext(context.Background(), sessionPath, limits, hasher)
}

func loadSessionMessagesWithContext(ctx context.Context, sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher) (msgs []provider.Message, fromEvents, damaged bool, err error) {
	res, err := loadSessionTranscript(ctx, sessionPath, limits, hasher)
	return res.msgs, res.fromEvents, res.damaged, err
}

// loadSessionTranscript accepts only the current DAG schema and materializes
// its selected head.
func loadSessionTranscript(ctx context.Context, sessionPath string, limits sessionReplayLimits, hasher *sessionTranscriptHasher) (sessionLoadResult, error) {
	if err := ctx.Err(); err != nil {
		return sessionLoadResult{}, err
	}
	probe, err := probeSessionEventLogWithLimits(sessionPath, limits)
	if err != nil {
		return sessionLoadResult{}, err
	}
	if probe.futureSchema {
		return sessionLoadResult{fromEvents: true}, fmt.Errorf("session event log for %s uses schema %d; this build supports up to %d", sessionPath, probe.schemaVersion, sessionDAGSchemaVersion)
	}
	if probe.dag {
		st, err := replaySessionDAG(ctx, store.SessionEventLog(sessionPath), limits)
		if err != nil {
			return sessionLoadResult{fromEvents: true, dag: true}, err
		}
		headID := st.selectedHead()
		msgs, times := st.materialize(headID)
		hasher.addAll(msgs)
		return sessionLoadResult{
			msgs: msgs, times: times, fromEvents: true, damaged: st.damaged || st.holes != 0, dag: true,
			head:      HeadRef{HeadID: headID, LeafID: st.heads[headID].leaf, LogGeneration: st.generation, LogOffset: st.lastGoodEnd},
			headCount: len(st.heads),
			state:     st,
			openTurn:  st.heads[headID].openTurn,
			events:    loadHeadEvents(st, headID),
		}, nil
	}
	if probe.size == 0 {
		return sessionLoadResult{}, os.ErrNotExist
	}
	return sessionLoadResult{}, fmt.Errorf("unsupported session log schema %d: current schema is %d", probe.schemaVersion, sessionDAGSchemaVersion)
}

func loadSessionMessagesFromJSONL(path string, hasher *sessionTranscriptHasher) ([]provider.Message, error) {
	return loadSessionMessagesFromJSONLContext(context.Background(), path, hasher)
}

func loadSessionMessagesFromJSONLContext(ctx context.Context, path string, hasher *sessionTranscriptHasher) ([]provider.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var msgs []provider.Message
	dec := json.NewDecoder(&contextReader{ctx: ctx, reader: f})
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var m provider.Message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		msgs = append(msgs, hasher.add(m))
	}
	return msgs, nil
}
