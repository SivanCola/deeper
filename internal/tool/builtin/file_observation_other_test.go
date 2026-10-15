//go:build !windows

package builtin

import "testing"

// Unix ctime records the write even when the fixture restores mtime.
func advanceObservationChangeTime(t *testing.T, _ string) { t.Helper() }
