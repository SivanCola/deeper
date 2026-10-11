package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"deepzora/internal/topicstate"
)

func (m *topicStateManager) createTopic(workspaceRoot, topicID, title, source string, createdAt int64) error {
	topicID, title, source = strings.TrimSpace(topicID), strings.TrimSpace(title), strings.TrimSpace(source)
	return m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.Update(ctx, topicID, func(record *topicstate.Record) {
			applyTopicTitle(record, title, source)
			record.CreatedAtMS = createdAt
		})
	})
}

func (m *topicStateManager) applyAutoTitle(workspaceRoot, topicID, title string, value topicAutoTitleMeta) (bool, error) {
	topicID, title = strings.TrimSpace(topicID), strings.TrimSpace(title)
	applied := false
	err := m.mutate(workspaceRoot, func(ctx context.Context, store *topicstate.Store) (topicstate.State, error) {
		return store.Update(ctx, topicID, func(record *topicstate.Record) {
			if record.TitleSource != topicTitleSourceAuto {
				return
			}
			applyTopicTitle(record, title, topicTitleSourceAuto)
			record.AutoMeta = mergeKnownAutoMeta(record.AutoMeta, value)
			applied = true
		})
	})
	return applied, err
}

func applyTopicTitle(record *topicstate.Record, title, source string) {
	record.Title = title
	if title == "" || source == "" {
		record.TitleSource = ""
	} else {
		record.TitleSource = source
	}
	if source == topicTitleSourceManual || (source == topicTitleSourceAuto && isDefaultTopicTitle(title)) {
		record.AutoMeta = nil
	}
}

func topicStateReadable(workspaceRoot string) error {
	if _, err := desktopTopicState.snapshot(workspaceRoot); err != nil {
		var future *topicstate.FutureSchemaError
		if errors.As(err, &future) {
			return errors.New("topic metadata was written by a newer Deepzora version; upgrade Deepzora to open it safely")
		}
		return fmt.Errorf("topic metadata is unavailable (%s); retry or check the Deepzora state directory permissions", topicStateErrorType(err))
	}
	return nil
}
