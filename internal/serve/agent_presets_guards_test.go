package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
)

func TestAgentPresetHTTPGuardsAuthIdentityAndBody(t *testing.T) {
	f := newPresetHTTPFixture(t, false)
	handler := f.server.Handler()
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if rec := gateDo(t, handler, method, "/agent-presets/quiet", `{"scope":"project","content":"body"}`, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("unauthenticated %s = %d", method, rec.Code)
		}
		if rec := gateDo(t, handler, method, "/agent-presets/quiet", `{}`, map[string]string{"Authorization": "Bearer preset-token", "Content-Type": "text/plain"}); rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("non-JSON %s = %d", method, rec.Code)
		}
	}
	for _, path := range []string{"/agent-preset", "/new", "/agent-presets"} {
		method, body := http.MethodPost, `{"agentPreset":"default"}`
		if path == "/agent-presets" {
			method, body = http.MethodGet, ""
		}
		rec := gateDo(t, handler, method, path, body, map[string]string{"Authorization": "Bearer preset-token", expectedSessionIDHeader: "stale"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("stale %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		body := `{"scope":"project"}`
		if method == http.MethodPut {
			body = `{"scope":"project","content":"body"}`
		}
		rec := gateDo(t, handler, method, "/agent-presets/quiet", body, map[string]string{"Authorization": "Bearer preset-token", expectedSessionIDHeader: "stale"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("stale author %s = %d", method, rec.Code)
		}
	}
	for _, path := range []string{"/agent-preset", "/new"} {
		for _, body := range []string{`{"agentPreset":1}`, `{"agentPreset":"default"} {}`, `{"unknown":true}`} {
			rec := gateDo(t, handler, http.MethodPost, path, body, map[string]string{"Authorization": "Bearer preset-token"})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid %s %s = %d", path, body, rec.Code)
			}
		}
	}
	body, err := json.Marshal(map[string]string{"scope": "project", "content": strings.Repeat("x", foregroundMutationMaxBody)})
	if err != nil {
		t.Fatal(err)
	}
	rec := gateDo(t, handler, http.MethodPut, "/agent-presets/quiet", string(body), map[string]string{"Authorization": "Bearer preset-token"})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized authoring = %d", rec.Code)
	}
	if list := f.list(t); len(list.Presets) != 1 || list.Current != "default" {
		t.Fatalf("refused authoring changed catalog: %+v", list)
	}
}

func TestAgentPresetHTTPFailedCandidateLeavesCurrentBlankSessionUnchanged(t *testing.T) {
	f := newPresetHTTPFixture(t, false)
	f.save(t, "project", "quiet", presetHTTPBody)
	before := f.server.ctl().(*control.Controller)
	ref, _ := before.SessionRef()
	snapshot, err := f.service.Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	f.server.rebuildControllerWithOptions = func(ctx context.Context, old *control.Controller, _ string, opts boot.Options) (*control.Controller, error) {
		result, err := boot.Rebuild(ctx, old, opts)
		if err != nil {
			return nil, err
		}
		next := result.Controller
		if err := os.WriteFile(config.UserConfigPath(), []byte("default_model = \"new/model\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return next, nil
	}
	f.request(t, http.MethodPost, "/agent-preset", map[string]string{"agentPreset": "quiet"}, http.StatusConflict)
	if f.server.ctl() != before || before.AgentPreset() != "" {
		t.Fatal("rejected candidate changed current controller")
	}
	after, err := f.service.Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if after.Projection.AgentPreset != "" || after.Projection.ModelMessages[0].Content != snapshot.Projection.ModelMessages[0].Content {
		t.Fatal("rejected candidate changed durable persona or prompt")
	}
	f.server.buildControllerWithOptions = func(context.Context, string, boot.Options) (*control.Controller, error) {
		return nil, errors.New("fixture build failure")
	}
	f.request(t, http.MethodPost, "/new", map[string]string{"agentPreset": "quiet"}, http.StatusBadRequest)
	if f.server.ctl() != before {
		t.Fatal("failed new candidate changed current controller")
	}
}
