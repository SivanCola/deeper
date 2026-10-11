package main

import (
	"context"

	"deepzora/internal/session"
)

type recordingTopicInfoReader struct {
	ids    []string
	onStat func()
}

func (r *recordingTopicInfoReader) Stat(_ context.Context, ref session.SessionRef) (session.SessionInfo, error) {
	r.ids = append(r.ids, ref.SessionID)
	if r.onStat != nil {
		r.onStat()
	}
	return session.SessionInfo{SessionID: ref.SessionID}, nil
}
