package control

import (
	"context"
	"deepzora/internal/session"
	"errors"

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
	reader := runtimeTranscriptReader{service: service, runtime: runtime}
	return reader.TranscriptFollow(ctx, req)
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
