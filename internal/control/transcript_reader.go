package control

import (
	"context"
	"errors"
	"time"

	"deepzora/internal/session"
	"deepzora/internal/transcript"
)

// NewRuntimeTranscriptReader captures a host-owned transcript independently of
// the replaceable Agent controller. The host must fence responses against the
// same Runtime and surface binding before publishing them.
func NewRuntimeTranscriptReader(service *session.Service, runtime *session.Runtime) TranscriptProjectionAPI {
	return runtimeTranscriptReader{service: service, runtime: runtime}
}

type runtimeTranscriptReader struct {
	service *session.Service
	runtime *session.Runtime
}

func (r runtimeTranscriptReader) TranscriptContent(req transcript.ContentRequest) (transcript.ContentChunk, error) {
	return r.runtime.Transcript().Content(req)
}

func (r runtimeTranscriptReader) TranscriptFollow(ctx context.Context, req transcript.FollowRequest) (TranscriptFollowResponse, error) {
	view, err := r.runtime.FollowTranscript(ctx, req)
	out := TranscriptFollowResponse{FollowResponse: view}
	if err != nil || view.Snapshot == nil {
		return out, err
	}
	// Make the frozen cut pageable even when accepted batches exceed the tail.
	// The registered subscription queues concurrent commits during flush/read;
	// no publisher lock is held.
	cut := view.Snapshot.CoveredThroughSeq
	if view.Snapshot.DurableSeq < cut {
		receipt, flushErr := r.runtime.Session().Flush(ctx)
		if flushErr != nil || receipt.DurableSequence < cut {
			_, _ = r.runtime.Transcript().Follow(context.Background(), transcript.FollowRequest{Subscription: view.Subscription, Close: true})
			if flushErr == nil {
				flushErr = errors.New("transcript snapshot persistence is incomplete")
			}
			return out, flushErr
		}
		// Publish the new watermark in order, after already queued frames. Do
		// not attach it to the older frozen view and then replay older watermarks.
		r.runtime.Transcript().SetDurableSequence(receipt.DurableSequence)
	}
	var page session.HistoryWindowPage
	for {
		page, err = r.service.Query().ReadHistoryWindow(ctx, r.runtime.Ref(), session.HistoryWindowRequest{Anchor: "newest", Limit: 32, SnapshotSequence: &cut})
		if err != nil || page.Status != "preparing" {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
		case <-timer.C:
		}
		if err != nil {
			break
		}
	}
	if err == nil {
		out.RecordPositions, err = currentFollowRecordPositions(ctx, r.service, r.runtime.Ref(), *view.Snapshot, page)
	}
	if err != nil {
		_, _ = r.runtime.Transcript().Follow(context.Background(), transcript.FollowRequest{Subscription: view.Subscription, Close: true})
	}
	out.History = &page
	return out, err
}
