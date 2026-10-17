package boot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"deepzora/internal/agentpreset"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/plugin"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func presetEffectSetup(t *testing.T) (Options, *effectRecordingProvider) {
	t.Helper()
	isolateConfigHome(t)
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	t.Setenv("DEEPZORA_CACHE_HOME", t.TempDir())
	fenceBootTestHistoryCatalog(t)
	root := robustTempDir(t)
	rec := &effectRecordingProvider{}
	kind := "preset-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return rec, nil })
	writeFile(t, root, "deepzora.toml", `
default_model = "test-model"
[agent]
system_prompt = "BASE CODING"
[environment]
enabled = false
[[providers]]
name = "test-model"
kind = "`+kind+`"
models = ["x"]
`)
	approveWorkspace(t, root)
	opts := withTestSession(t, Options{WorkspaceRoot: root, SessionDir: filepath.Join(root, "sessions"), Sink: event.Discard})
	return opts, rec
}

func buildPresetEffect(t *testing.T, opts Options) *BuildResult {
	t.Helper()
	result, err := BuildRuntime(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(result.Controller.Close)
	return result
}

func TestAgentPresetSourceAndOverrideSelection(t *testing.T) {
	opts, _ := presetEffectSetup(t)
	if _, err := agentpreset.Write(AgentPresetDirs(opts.WorkspaceRoot).Project, "review", "Review persona"); err != nil {
		t.Fatal(err)
	}
	first := buildPresetEffect(t, opts)
	ref, err := first.Controller.BindFreshSession(t.Context(), "selection-source")
	if err != nil {
		t.Fatal(err)
	}
	_, runtime, _ := first.Controller.SessionBinding()
	opts.SessionRuntime, opts.AgentPreset = runtime, "review"
	p, err := resolveAgentPreset(t.Context(), opts.WorkspaceRoot, opts, event.Discard)
	if err != nil || !p.IsDefault() {
		t.Fatalf("nil override did not inherit recorded blank default: %+v %v", p, err)
	}
	selection := "review"
	opts.AgentPresetOverride = &selection
	p, err = resolveAgentPreset(t.Context(), opts.WorkspaceRoot, opts, event.Discard)
	if err != nil || p.ID != "review" {
		t.Fatalf("blank override=%+v %v", p, err)
	}
	opts.SessionRuntime, opts.AgentPresetSource = nil, &ref
	p, err = resolveAgentPreset(t.Context(), opts.WorkspaceRoot, opts, event.Discard)
	if err != nil || p.ID != "review" {
		t.Fatalf("source blank override=%+v %v", p, err)
	}
	missing := ref
	missing.SessionID = "does-not-exist"
	opts.AgentPresetSource = &missing
	if _, err := resolveAgentPreset(t.Context(), opts.WorkspaceRoot, opts, event.Discard); err == nil {
		t.Fatal("missing source was silently treated as a fresh session")
	}
	opts.AgentPresetSource, opts.SessionService = &ref, nil
	if _, err := resolveAgentPreset(t.Context(), opts.WorkspaceRoot, opts, event.Discard); err == nil {
		t.Fatal("source accepted missing query service")
	}
}

func TestEffectAgentPresetPromptAndSchemasRemainStableAcrossTurns(t *testing.T) {
	opts, rec := presetEffectSetup(t)
	if _, err := agentpreset.Write(AgentPresetDirs(opts.WorkspaceRoot).Project, "review", "---\ntools: [read_file]\n---\nREVIEW PERSONA"); err != nil {
		t.Fatal(err)
	}
	opts.AgentPreset = "review"
	result := buildPresetEffect(t, opts)
	for _, input := range []string{"first", "second"} {
		if err := result.Controller.Run(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	reqs := rec.requests()
	if len(reqs) != 2 || len(reqs[0].Messages) == 0 || !strings.Contains(reqs[0].Messages[0].Content, "BASE CODING") || !strings.Contains(reqs[0].Messages[0].Content, "REVIEW PERSONA") {
		t.Fatalf("preset provider requests=%+v", reqs)
	}
	if !reflect.DeepEqual(reqs[0].Messages[0], reqs[1].Messages[0]) || !reflect.DeepEqual(reqs[0].Tools, reqs[1].Tools) {
		t.Fatal("preset prompt or schema changed across ordinary turns")
	}
	if names := toolNames(reqs[0]); len(names) != 2 || !names["read_file"] || !names["use_capability"] {
		t.Fatalf("restricted provider tools=%v", names)
	}
	if !result.Controller.AgentPresetLocked() {
		t.Fatal("provider turn did not lock preset")
	}
	selection := "default"
	if _, err := Rebuild(t.Context(), result.Controller, Options{WorkspaceRoot: opts.WorkspaceRoot, Sink: event.Discard, AgentPresetOverride: &selection}); !errors.Is(err, control.ErrAgentPresetLocked) {
		t.Fatalf("locked override=%v", err)
	}
}

func TestEffectAgentPresetDefaultSelectionLeavesPromptAndSchemasUnchanged(t *testing.T) {
	opts, rec := presetEffectSetup(t)
	plain := buildPresetEffect(t, opts)
	if err := plain.Controller.Run(t.Context(), "plain"); err != nil {
		t.Fatal(err)
	}
	opts.AgentPreset = "default"
	explicit := buildPresetEffect(t, opts)
	if err := explicit.Controller.Run(t.Context(), "explicit"); err != nil {
		t.Fatal(err)
	}
	reqs := rec.requests()
	if len(reqs) != 2 || reqs[0].Messages[0].Content != reqs[1].Messages[0].Content || !reflect.DeepEqual(reqs[0].Tools, reqs[1].Tools) {
		t.Fatal("explicit default changed provider prefix or schemas")
	}
	if explicit.Controller.AgentPreset() != "" {
		t.Fatal("default identity was not canonicalized")
	}
}

func TestEffectAgentPresetRestoreUsesLatestDefinitionAndPreservesForkIdentity(t *testing.T) {
	opts, rec := presetEffectSetup(t)
	dirs := AgentPresetDirs(opts.WorkspaceRoot)
	if _, err := agentpreset.Write(dirs.Project, "review", "---\nkeep-coding-instructions: false\ntools: [read_file]\n---\nOLD PERSONA"); err != nil {
		t.Fatal(err)
	}
	opts.AgentPreset = "review"
	first := buildPresetEffect(t, opts)
	if err := first.Controller.Run(t.Context(), "original"); err != nil {
		t.Fatal(err)
	}
	ref, _ := first.Controller.SessionRef()
	_, runtime, _ := first.Controller.SessionBinding()
	if err := first.Controller.Snapshot(); err != nil {
		t.Fatal(err)
	}
	before := runtime.Session().Snapshot()
	if err := first.Controller.ReleaseSessionRuntimeBinding(); err != nil {
		t.Fatal(err)
	}
	if err := opts.SessionService.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := agentpreset.Write(dirs.Project, "review", "---\nkeep-coding-instructions: false\ntools: []\n---\nLATEST PERSONA"); err != nil {
		t.Fatal(err)
	}
	opts.AgentPreset, opts.AgentPresetSource = "default", &ref
	restored := buildPresetEffect(t, opts)
	if _, bound := restored.Controller.SessionRef(); bound {
		t.Fatal("preset source bound controller during assembly")
	}
	if restored.Controller.AgentPreset() != "review" {
		t.Fatalf("source selection=%q", restored.Controller.AgentPreset())
	}
	if _, err := restored.Controller.OpenSession(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if err := restored.Controller.Run(t.Context(), "restored"); err != nil {
		t.Fatal(err)
	}
	reqs := rec.requests()
	latest := reqs[len(reqs)-1]
	if !strings.HasPrefix(latest.Messages[0].Content, "LATEST PERSONA\n\n") || strings.Contains(latest.Messages[0].Content, "BASE CODING") || len(latest.Tools) != 1 || latest.Tools[0].Name != "use_capability" {
		t.Fatalf("restored provider did not receive latest preset: system=%q tools=%v", latest.Messages[0].Content, toolSchemaNames(latest.Tools))
	}
	after, err := opts.SessionService.Query().Snapshot(t.Context(), ref)
	if err != nil || after.Projection.Messages[0].Content != before.Projection.Messages[0].Content {
		t.Fatalf("custom refresh changed canonical history: %v", err)
	}
	_, current, _ := restored.Controller.SessionBinding()
	childDir := filepath.Join(t.TempDir(), "child")
	if _, err := current.Session().Fork(t.Context(), childDir, "preset-child", after.EventSequence); err != nil {
		t.Fatal(err)
	}
	child, err := session.Open(childDir, "preset-child")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close(context.Background())
	if child.ExecutionSnapshot().Projection.AgentPreset != "review" {
		t.Fatal("fork lost recorded preset identity")
	}
	if err := os.Remove(filepath.Join(dirs.Project, "review.md")); err != nil {
		t.Fatal(err)
	}
	missing := buildPresetEffect(t, opts)
	if missing.Controller.AgentPreset() != "review" || !strings.HasPrefix(missing.Controller.SystemPrompt(), "BASE CODING\n\n") {
		t.Fatalf("missing preset fallback lost identity or default behavior: %q %q", missing.Controller.AgentPreset(), missing.Controller.SystemPrompt())
	}
}

func TestEffectAgentPresetEditedDefinitionForcesRebuildByID(t *testing.T) {
	opts, rec := presetEffectSetup(t)
	dir := AgentPresetDirs(opts.WorkspaceRoot).Project
	if _, err := agentpreset.Write(dir, "review", "---\ntools: [read_file]\n---\nOLD PERSONA"); err != nil {
		t.Fatal(err)
	}
	opts.AgentPreset = "review"
	first := buildPresetEffect(t, opts)
	if err := first.Controller.Run(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := agentpreset.Write(dir, "review", "---\ntools: []\n---\nLATEST PERSONA"); err != nil {
		t.Fatal(err)
	}
	next, err := RebuildFrom(t.Context(), first, Options{WorkspaceRoot: opts.WorkspaceRoot, Sink: event.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Controller.Close)
	if next.ReusedController || next.Controller == first.Controller {
		t.Fatal("edited preset reused stale controller")
	}
	if err := control.ActivateControllerReplacement(first.Controller, next.Controller); err != nil {
		t.Fatal(err)
	}
	if err := next.Controller.Run(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	reqs := rec.requests()
	latest := reqs[len(reqs)-1]
	if strings.Contains(latest.Messages[0].Content, "OLD PERSONA") || !strings.Contains(latest.Messages[0].Content, "LATEST PERSONA") || len(latest.Tools) != 1 {
		t.Fatal("edited definition did not reach provider after rebuild")
	}
}

func TestEffectAgentPresetBlocksMCPStartupAndManualConnection(t *testing.T) {
	opts, _ := presetEffectSetup(t)
	if _, err := agentpreset.Write(AgentPresetDirs(opts.WorkspaceRoot).Project, "talk", "---\ntools: []\n---\nTalk only"); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	opts.AgentPreset = "talk"
	opts.ExtraPlugins = []plugin.Spec{{Name: "blocked", Type: "http", URL: server.URL, Authorized: true}}
	result := buildPresetEffect(t, opts)
	entry := config.PluginEntry{Name: "blocked", Type: "http", URL: server.URL, Source: config.MCPSourceUserConfig}
	if _, err := result.Controller.ConnectMCPServer(entry); err == nil {
		t.Fatal("manual connection bypassed preset ceiling")
	}
	if _, err := result.Controller.RegisterMCPServerOnDemand(entry); err == nil {
		t.Fatal("on-demand registration bypassed preset ceiling")
	}
	if result.Controller.Host().HasClient("blocked") || requests.Load() != 0 {
		t.Fatalf("excluded MCP server received %d startup requests", requests.Load())
	}
}

func TestEffectAgentPresetBlankOverrideCommitsAtActivation(t *testing.T) {
	opts, rec := presetEffectSetup(t)
	if _, err := agentpreset.Write(AgentPresetDirs(opts.WorkspaceRoot).Project, "review", "---\nkeep-coding-instructions: false\ntools: []\n---\nONLY REVIEW"); err != nil {
		t.Fatal(err)
	}
	opts.AgentPreset = "review"
	first := buildPresetEffect(t, opts)
	ref, err := first.Controller.BindFreshSession(t.Context(), "blank")
	if err != nil {
		t.Fatal(err)
	}
	_, runtime, _ := first.Controller.SessionBinding()
	before := runtime.Session().ExecutionSnapshot()
	selection := ""
	next, err := RebuildFrom(t.Context(), first, Options{WorkspaceRoot: opts.WorkspaceRoot, Sink: event.Discard, AgentPresetOverride: &selection})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Controller.Close)
	if runtime.Session().ExecutionSnapshot().EventSequence != before.EventSequence || runtime.Session().ExecutionSnapshot().Projection.AgentPreset != "review" {
		t.Fatal("candidate modified live blank selection before activation")
	}
	if err := control.ActivateControllerReplacement(first.Controller, next.Controller); err != nil {
		t.Fatal(err)
	}
	if next.Controller.AgentPreset() != "" || runtime.Session().ExecutionSnapshot().Projection.AgentPreset != "" {
		t.Fatal("explicit empty override failed to reset default")
	}
	if got, ok := next.Controller.SessionRef(); !ok || got != ref {
		t.Fatal("preset switch changed immutable session identity")
	}
	if err := next.Controller.Run(t.Context(), "after selection"); err != nil {
		t.Fatal(err)
	}
	reqs := rec.requests()
	if len(reqs) != 1 || !strings.HasPrefix(reqs[0].Messages[0].Content, "BASE CODING\n\n") || strings.Contains(reqs[0].Messages[0].Content, "ONLY REVIEW") || len(reqs[0].Tools) <= 1 {
		t.Fatalf("default replacement did not reach provider: requests=%d", len(reqs))
	}
}
