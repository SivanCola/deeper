package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/control"
)

func TestAgentPresetHTTPFailedResumeLeavesIdleOrBusyForeground(t *testing.T) {
	for _, busy := range []bool{false, true} {
		name := "idle"
		if busy {
			name = "busy"
		}
		t.Run(name, func(t *testing.T) {
			f := newPresetHTTPFixture(t, false)
			f.save(t, "project", "quiet", presetHTTPBody)
			f.request(t, http.MethodPost, "/new", map[string]string{"agentPreset": "quiet"}, http.StatusNoContent)
			f.run(t)
			ref, _ := f.server.ctl().(*control.Controller).SessionRef()
			f.request(t, http.MethodPost, "/new", map[string]string{"agentPreset": "default"}, http.StatusNoContent)
			before := f.server.ctl().(*control.Controller)
			current, _ := before.SessionRef()
			if busy {
				f.provider.hold = make(chan struct{})
				startPresetHTTPHeldTurn(t, f)
				defer func() { before.Cancel(); waitNotRunning(t, before) }()
			}
			path := filepath.Join(before.WorkspaceRoot(), ".deepzora", "presets", "quiet.md")
			if err := os.WriteFile(path, []byte("---\ntool: []\n---\ninvalid"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{ref.SessionID, "missing"} {
				f.request(t, http.MethodPost, "/resume", map[string]string{"hostId": ref.HostID, "sessionId": target}, http.StatusConflict)
				if f.server.ctl() != before {
					t.Fatal("failed resume replaced foreground")
				}
				if got, _ := before.SessionRef(); got != current || before.AgentPreset() != "" || before.Running() != busy {
					t.Fatalf("failed resume changed foreground: %+v preset=%q running=%v", got, before.AgentPreset(), before.Running())
				}
			}
		})
	}
}
