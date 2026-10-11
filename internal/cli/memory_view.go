package cli

import (
	"time"

	"deepzora/internal/control"
	"deepzora/internal/memory"
)

func renderMemory(width int, set *memory.Set) string {
	return viewProtectLines(control.RenderMemorySummary(set, time.Now().UTC()), width)
}
