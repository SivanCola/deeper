package agent

import (
	"context"
	"strings"
	"unicode/utf8"

	"deepzora/internal/provider"
)

const (
	toolPruneThresholdRunes = 8192
	toolPruneHeadRunes      = 4096
	toolPruneTailRunes      = 1024
	toolPruneMarker         = "[... tool result middle pruned ...]"
)

func pruneToolResultContent(content string) (string, bool) {
	if utf8.RuneCountInString(content) <= toolPruneThresholdRunes {
		return content, false
	}
	headEnd := byteOffsetAfterRunes(content, toolPruneHeadRunes)
	tailStart := byteOffsetBeforeLastRunes(content, toolPruneTailRunes)
	var pruned strings.Builder
	pruned.Grow(headEnd + len(toolPruneMarker) + len(content) - tailStart)
	pruned.WriteString(content[:headEnd])
	pruned.WriteString(toolPruneMarker)
	pruned.WriteString(content[tailStart:])
	return pruned.String(), true
}

func byteOffsetAfterRunes(content string, count int) int {
	if count <= 0 {
		return 0
	}
	seen := 0
	for offset := range content {
		if seen == count {
			return offset
		}
		seen++
	}
	return len(content)
}

func byteOffsetBeforeLastRunes(content string, count int) int {
	offset := len(content)
	for range count {
		if offset == 0 {
			return 0
		}
		_, size := utf8.DecodeLastRuneInString(content[:offset])
		offset -= size
	}
	return offset
}

// pruneToolResultsToProjectionLocked installs a durable, model-visible prune
// projection. The caller owns compactionRunMu for the whole maintenance run;
// canonical storage, including RawContent, is never modified.
func (a *Agent) pruneToolResultsToProjectionLocked(ctx context.Context, trigger string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	canonical, transcriptVersion := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	stateSnapshot := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	visible, _ := a.visibleInputForFold(stateSnapshot, canonical, transcriptVersion)
	projected := append([]provider.Message(nil), visible...)
	affected := 0
	for i := range projected {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if projected[i].Role != provider.RoleTool {
			continue
		}
		source := projected[i].Content
		if pruned, changed := pruneToolResultContent(source); changed {
			projected[i].Content = pruned
			projected[i].RawContent = ""
			affected++
		}
	}
	if affected == 0 {
		return false, nil
	}
	return a.installMaintenanceProjection(ctx, maintenanceInstall{
		trigger: trigger, action: "prune", state: stateSnapshot,
		canonical: canonical, transcriptVersion: transcriptVersion,
		visible: visible, projected: projected, affected: affected,
	})
}
