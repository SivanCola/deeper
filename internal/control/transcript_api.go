package control

import (
	"context"
	"deepzora/internal/session"
	"errors"
	"time"

	"deepzora/internal/transcript"
)

var ErrTranscriptProjectionUnavailable = errors.New("transcript projection is unavailable")

type TranscriptProjectionAPI interface {
	TranscriptContent(transcript.ContentRequest) (transcript.ContentChunk, error)
}

var _ TranscriptProjectionAPI = (*Controller)(nil)

type TranscriptFollowAPI interface {
	TranscriptFollow(context.Context, transcript.FollowRequest) (TranscriptFollowResponse, error)
}

type TranscriptFollowResponse struct {
	transcript.FollowResponse
	History         *session.HistoryWindowPage `json:"history,omitempty"`
	RecordPositions map[string]int64           `json:"recordPositions,omitempty"`
}

func (c *Controller) TranscriptFollow(ctx context.Context, req transcript.FollowRequest) (TranscriptFollowResponse, error) {
	service, runtime, exclusive := c.v3Binding()
	if !exclusive || service == nil || runtime == nil {
		return TranscriptFollowResponse{}, ErrTranscriptProjectionUnavailable
	}
	view, err := runtime.FollowTranscript(ctx, req)
	out := TranscriptFollowResponse{FollowResponse: view}
	if err != nil || view.Snapshot == nil {
		return out, err
	}
	// Make the frozen cut pageable even when accepted batches exceed the tail.
	// The registered subscription queues concurrent commits during flush/read;
	// no publisher lock is held.
	cut := view.Snapshot.CoveredThroughSeq
	if view.Snapshot.DurableSeq < cut {
		receipt, flushErr := runtime.Session().Flush(ctx)
		if flushErr != nil || receipt.DurableSequence < cut {
			_, _ = runtime.Transcript().Follow(context.Background(), transcript.FollowRequest{Subscription: view.Subscription, Close: true})
			if flushErr == nil {
				flushErr = errors.New("transcript snapshot persistence is incomplete")
			}
			return out, flushErr
		}
		// Publish the new watermark in order, after already queued frames. Do
		// not attach it to the older frozen view and then replay older watermarks.
		runtime.Transcript().SetDurableSequence(receipt.DurableSequence)
	}
	var page session.HistoryWindowPage
	for {
		page, err = service.Query().ReadHistoryWindow(ctx, runtime.Ref(), session.HistoryWindowRequest{Anchor: "newest", Limit: 32, SnapshotSequence: &cut})
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
		out.RecordPositions, err = currentFollowRecordPositions(ctx, service, runtime.Ref(), *view.Snapshot, page)
	}
	if err != nil {
		_, _ = runtime.Transcript().Follow(context.Background(), transcript.FollowRequest{Subscription: view.Subscription, Close: true})
	}
	out.History = &page
	return out, err
}

// SetTurnSubmissionID is called under the transport's admission boundary.
func (c *Controller) SetTurnSubmissionID(submissionID string) {
	if ledger := c.turnEventLedger(); ledger != nil {
		ledger.SetSubmissionID(submissionID)
	}
}

// BindTranscriptRuntimeEpoch runs at the surface's idle runtime publication
// boundary. It takes only display/ledger leaf locks and invokes no callbacks.
func (c *Controller) BindTranscriptRuntimeEpoch(epoch string) {
	c.turnEvents.commitMu.Lock()
	defer c.turnEvents.commitMu.Unlock()
	ledger := c.turnEventLedger()
	if ledger == nil || ledger.ActiveTurnID() != "" {
		return
	}
	ledger.SetRuntimeEpoch(epoch)
	c.turnEvents.mu.RLock()
	p := c.turnEvents.projection
	c.turnEvents.mu.RUnlock()
	if p != nil {
		p.SetRuntimeEpoch(epoch)
	}
}

func (c *Controller) transcriptProjection() (*transcript.Projection, error) {
	if _, runtime, exclusive := c.v3Binding(); exclusive && runtime != nil {
		return runtime.Transcript(), nil
	}
	c.turnEvents.mu.RLock()
	defer c.turnEvents.mu.RUnlock()
	if c.turnEvents.err != nil {
		return nil, errors.Join(ErrTranscriptProjectionUnavailable, c.turnEvents.err)
	}
	if c.turnEvents.projectionErr != nil {
		return nil, errors.Join(ErrTranscriptProjectionUnavailable, c.turnEvents.projectionErr)
	}
	if c.turnEvents.projection == nil {
		return nil, ErrTranscriptProjectionUnavailable
	}
	return c.turnEvents.projection, nil
}

func (c *Controller) TranscriptSnapshot(req transcript.PageRequest) (transcript.Snapshot, error) {
	p, err := c.transcriptProjection()
	if err != nil {
		return transcript.Snapshot{}, err
	}
	return p.Snapshot(req)
}

func (c *Controller) TranscriptContent(req transcript.ContentRequest) (transcript.ContentChunk, error) {
	p, err := c.transcriptProjection()
	if err != nil {
		return transcript.ContentChunk{}, err
	}
	return p.Content(req)
}
