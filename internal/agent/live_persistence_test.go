//go:build live

package agent

import (
	"deepzora/internal/agent/testutil"
	"deepzora/internal/provider"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestLiveCurrentPersistenceWriteCheckpoint(t *testing.T) {
	root := t.TempDir()
	args, _ := json.Marshal(map[string]string{"path": filepath.Join(root, "marker.txt"), "content": "live-write-marker"})
	p := testutil.NewMock("current-write", testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "write", Name: "write_file", Arguments: string(args)}}}, testutil.Turn{Text: "verified existing file"})
	runLiveWriteAfterEffectResume(t, p, "fixture", root)
}
