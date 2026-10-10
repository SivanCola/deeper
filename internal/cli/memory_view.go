package cli

import (
	"time"

	"deeper/internal/control"
	"deeper/internal/memory"
)

func renderMemory(width int, set *memory.Set) string {
	return viewProtectLines(control.RenderMemorySummary(set, time.Now().UTC()), width)
}
