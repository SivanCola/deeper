package main

import (
	"context"
	"errors"
	"os"

	"deepzora/internal/topicstate"
)

func topicStateErrorType(err error) string {
	if err == nil {
		return "none"
	}
	var future *topicstate.FutureSchemaError
	if errors.As(err, &future) {
		return "future_schema"
	}
	if topicstate.IsCorruptionError(err) {
		return "corruption"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission"
	}
	return "io"
}
