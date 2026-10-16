package control

import (
	"context"
	"fmt"
	"sort"
	"time"

	"deepzora/internal/event"
	"deepzora/internal/session"
)

// BranchInfo is the branch picker contract over the current session catalog.
// It contains no transcript paths, sidecar fields, or in-log head identity.
type BranchInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ParentID  string    `json:"parent_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Model     string    `json:"model"`
	Preview   string    `json:"preview"`
	Turns     int       `json:"turns"`
}

func (c *Controller) currentSessionBranches(ctx context.Context) ([]BranchInfo, error) {
	service := c.SessionService()
	if service == nil {
		return nil, session.ErrSessionNotRunning
	}
	var allowed map[string]bool
	if c.sessionReadScope != nil {
		var err error
		allowed, err = c.sessionReadScope(ctx)
		if err != nil {
			return nil, err
		}
	}
	branches := []BranchInfo{}
	for cursor := ""; ; {
		page, err := service.Query().List(ctx, cursor, 100)
		if err != nil {
			return nil, err
		}
		for _, info := range page.Sessions {
			if info.Error != "" || (c.sessionReadScope != nil && !allowed[info.SessionID]) {
				continue
			}
			// The branch selector is the current ID, never a directory or filename.
			branches = append(branches, BranchInfo{
				ID: info.SessionID, Name: info.Title, ParentID: info.ParentSessionID,
				CreatedAt: info.CreatedAt, UpdatedAt: info.UpdatedAt, Model: info.ModelRef,
				Preview: info.Preview, Turns: info.Turns,
			})
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor <= cursor {
			return nil, fmt.Errorf("%w: branch catalog cursor did not advance", session.ErrDamagedStore)
		}
		cursor = page.NextCursor
	}
	sort.Slice(branches, func(i, j int) bool {
		if branches[i].CreatedAt.Equal(branches[j].CreatedAt) {
			return branches[i].ID < branches[j].ID
		}
		return branches[i].CreatedAt.Before(branches[j].CreatedAt)
	})
	return branches, nil
}

// switchCurrentSessionBranch is called while the controller owns its rotation
// gate. Querying and validating the target do not acquire its writer.
func (c *Controller) switchCurrentSessionBranch(ctx context.Context, selector string) (BranchInfo, error) {
	fail := func(err error) (BranchInfo, error) { return BranchInfo{}, c.rewindFail(err) }
	branches, err := c.currentSessionBranches(ctx)
	if err != nil {
		return fail(err)
	}
	match, err := resolveBranch(branches, selector)
	if err != nil {
		return fail(err)
	}
	service := c.SessionService()
	ref, err := service.Query().ResolveSessionID(ctx, match.ID)
	if err != nil {
		return fail(err)
	}
	if c.resolveSessionModel != nil {
		snapshot, err := service.Query().Snapshot(ctx, ref)
		if err != nil {
			return fail(err)
		}
		if model := snapshot.Projection.ModelRef; model != "" {
			if _, err := c.resolveSessionModel(model, snapshot.Projection.ModelIdentity); err != nil {
				return fail(err)
			}
		}
	}
	if c.sessionReadScope != nil {
		allowed, err := c.sessionReadScope(ctx)
		if err != nil {
			return fail(err)
		}
		if !allowed[ref.SessionID] {
			return fail(fmt.Errorf("branch %q is no longer in the current scope", selector))
		}
	}
	if _, err := c.OpenSession(ctx, ref); err != nil {
		return fail(err)
	}
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("switched to branch %s", branchDisplayName(match))})
	return match, nil
}
