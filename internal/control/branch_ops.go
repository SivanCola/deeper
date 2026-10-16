package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/session"
)

// Fork creates an independent current session before the selected checkpoint's
// turn and switches to it. Code is unchanged. The checkpoint's stable TurnID
// determines the cut, including after a controller replacement.
func (c *Controller) Fork(turn int) (string, error) {
	return c.ForkNamed(turn, "")
}

func (c *Controller) ForkNamed(turn int, name string) (string, error) {
	return c.forkNamed(turn, name, true)
}

func (c *Controller) forkNamed(turn int, name string, switchToFork bool) (string, error) {
	if err := c.beginRotation(); err != nil {
		if errors.Is(err, errTurnRunningRotation) {
			return "", c.rewindFail(fmt.Errorf("cannot fork while a turn is running"))
		}
		return "", c.rewindFail(err)
	}
	defer c.endRotation()
	return c.forkNamedReady(turn, name, switchToFork)
}

// forkNamedReady runs under the rotation gate held by ForkNamed or rewind.
func (c *Controller) forkNamedReady(turn int, name string, switchToFork bool) (string, error) {
	if c.executor == nil {
		return "", c.rewindFail(fmt.Errorf("checkpoints unavailable"))
	}
	return c.rewindCheckpointSession(turn, name, switchToFork)
}

func (c *Controller) CheckpointHasBoundary(turn int) bool {
	boundary, ok := c.checkpoints.boundary(turn)
	if !ok || c.executor == nil {
		return false
	}
	return boundary <= c.executor.Session().Len()
}

// Branch copies the current conversation into a child branch and switches to it.
// Unlike Fork, it branches at the current tip and does not require a checkpoint.
func (c *Controller) Branch(name string) (string, error) {
	if c.executor == nil {
		return "", c.rewindFail(fmt.Errorf("branch unavailable"))
	}
	// Hold the rotation gate across the Snapshot and the switch below so a turn
	// cannot start mid-branch and then have its session replaced.
	if err := c.beginRotation(); err != nil {
		if errors.Is(err, errTurnRunningRotation) {
			return "", c.rewindFail(fmt.Errorf("cannot branch while a turn is running"))
		}
		return "", c.rewindFail(err)
	}
	defer c.endRotation()
	service, runtime, _ := c.v3Binding()
	if service == nil || runtime == nil {
		return "", c.rewindFail(session.ErrSessionNotRunning)
	}
	turns := runtime.Session().ExecutionSnapshot().Projection.Turns
	completed := 0
	for _, turn := range turns {
		if turn.EndSequence != 0 {
			completed++
		}
	}
	if completed == 0 {
		return "", c.rewindFail(fmt.Errorf("nothing to branch yet"))
	}
	child, err := service.Branch(context.Background(), runtime.Ref(), "")
	if err != nil {
		return "", c.rewindFail(err)
	}
	return c.finishSessionFork(service, child, name, true)
}

func (c *Controller) rewindCheckpointSession(turn int, name string, switchToFork bool) (string, error) {
	service, parent, _ := c.v3Binding()
	if service == nil || parent == nil {
		return "", session.ErrSessionNotRunning
	}
	c.checkpoints.mu.Lock()
	turnID := c.checkpoints.turnIDs[turn]
	c.checkpoints.mu.Unlock()
	if turnID == "" {
		return "", fmt.Errorf("rewind checkpoint %d has no current turn identity", turn)
	}
	child, err := service.Rewind(context.Background(), parent.Ref(), turnID, "")
	if err != nil {
		return "", err
	}
	return c.finishSessionFork(service, child, name, switchToFork)
}

func (c *Controller) finishSessionFork(service *session.Service, child *session.Runtime, name string, switchToFork bool) (string, error) {
	closeChild := true
	defer func() {
		if closeChild {
			_ = service.Close(context.Background(), child.Ref())
		}
	}()
	if title := strings.TrimSpace(name); title != "" {
		payload, marshalErr := json.Marshal(map[string]string{"title": title})
		if marshalErr != nil {
			return "", marshalErr
		}
		if _, appendErr := child.Session().AppendBatch(context.Background(), "fork-title:"+child.Ref().SessionID, []session.Event{{Kind: "session/title", Payload: payload}}); appendErr != nil {
			return "", appendErr
		}
	}
	if _, err := child.Session().Flush(context.Background()); err != nil {
		return "", err
	}
	if !switchToFork {
		return child.Ref().SessionID, nil
	}
	prepared := agent.NewSession("").CloneWithMessages(child.Session().ExecutionSnapshot().Projection.ModelMessages)
	_, err := c.publishSessionRuntime(child, prepared, true)
	if err != nil {
		return "", err
	}
	closeChild = false
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("forked conversation into session %s", child.Ref().SessionID)})
	return child.Ref().SessionID, nil
}

// Branches lists saved conversation branches in the current host scope.
func (c *Controller) Branches() ([]BranchInfo, error) {
	return c.currentSessionBranches(context.Background())
}

func (c *Controller) SwitchBranch(ref string) (BranchInfo, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return BranchInfo{}, c.rewindFail(fmt.Errorf("usage: /switch <branch id|name>"))
	}
	// Hold the rotation gate across target validation and attachment so a turn
	// cannot start while the controller changes its session binding.
	if err := c.beginRotation(); err != nil {
		if errors.Is(err, errTurnRunningRotation) {
			return BranchInfo{}, c.rewindFail(fmt.Errorf("cannot switch branches while a turn is running"))
		}
		return BranchInfo{}, c.rewindFail(err)
	}
	defer c.endRotation()
	return c.switchCurrentSessionBranch(context.Background(), ref)
}

func resolveBranch(branches []BranchInfo, ref string) (BranchInfo, error) {
	refLower := strings.ToLower(ref)
	var matches []BranchInfo
	for _, b := range branches {
		nameLower := strings.ToLower(strings.TrimSpace(b.Name))
		switch {
		case b.ID == ref || strings.EqualFold(b.ID, ref):
			return b, nil
		case b.Name != "" && nameLower == refLower:
			matches = append(matches, b)
		case strings.HasPrefix(strings.ToLower(b.ID), refLower):
			matches = append(matches, b)
		case strings.HasPrefix(strings.ToLower(shortBranchID(b.ID)), refLower):
			matches = append(matches, b)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return BranchInfo{}, fmt.Errorf("branch %q is ambiguous", ref)
	}
	return BranchInfo{}, fmt.Errorf("branch %q not found", ref)
}

func branchDisplayName(b BranchInfo) string {
	if strings.TrimSpace(b.Name) != "" {
		return fmt.Sprintf("%s (%s)", b.Name, b.ID)
	}
	return b.ID
}
