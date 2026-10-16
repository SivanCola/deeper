package control

import (
	"context"
	"errors"
	"slices"

	"deepzora/internal/session"
	"deepzora/internal/transcript"
)

// Snapshot records can precede the bounded durable tail. Resolve their positions
// at that same fixed cut without fetching bodies or treating display indexes as
// canonical positions. Provisional output has no durable position yet.
func currentFollowRecordPositions(ctx context.Context, service *session.Service, ref session.SessionRef, snapshot transcript.Snapshot, page session.HistoryWindowPage) (map[string]int64, error) {
	present := make(map[string]bool, len(page.Messages))
	for _, message := range page.Messages {
		present[message.MessageID] = true
	}
	positions := map[string]int64{}
	for _, record := range slices.Concat(snapshot.Records, snapshot.ActiveRecords) {
		id := record.Message.MessageID
		if id == "" || present[id] {
			continue
		}
		present[id] = true
		location, err := service.Query().LocateMessage(ctx, ref, id, snapshot.CoveredThroughSeq)
		if err != nil {
			return nil, err
		}
		if location.Generation != page.Generation || location.SnapshotSequence != page.SnapshotSequence {
			return nil, errors.New("transcript history changed while locating active records")
		}
		switch location.Status {
		case "ready":
			positions[id] = location.Position
		case "not_found":
		default:
			return nil, errors.New("transcript active record position is not ready")
		}
	}
	return positions, nil
}
