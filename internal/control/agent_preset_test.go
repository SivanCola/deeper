package control

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agentpreset"
	"deepzora/internal/event"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func presetController(t *testing.T, service *session.Service, runtime *session.Runtime, id, prompt string) *Controller {
	t.Helper()
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession(prompt), agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: exec, Sink: event.Discard, SystemPrompt: prompt, AgentPreset: id,
		SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(c.Close)
	return c
}

func presetService(t *testing.T) *session.Service {
	t.Helper()
	service, err := session.NewService("local", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	return service
}

func TestAgentPresetReplacementStagesIdentityWithModelContext(t *testing.T) {
	service := presetService(t)
	first := presetController(t, service, nil, "review", "OLD PERSONA")
	ref, err := first.BindFreshSession(t.Context(), "preset-cutover")
	if err != nil {
		t.Fatal(err)
	}
	_, runtime, _ := first.SessionBinding()
	before := runtime.Session().Snapshot()
	next := presetController(t, service, runtime, "", "DEFAULT PERSONA")
	if err := next.AdoptRebuiltModelContext(next.agentPresetModelContext(before.Projection.ModelMessages)); err != nil {
		t.Fatal(err)
	}
	if after := runtime.Session().Snapshot(); !reflect.DeepEqual(before.Projection, after.Projection) || before.EventSequence != after.EventSequence {
		t.Fatal("preparation mutated live session")
	}
	if err := ActivateControllerReplacement(first, next); err != nil {
		t.Fatal(err)
	}
	after := runtime.Session().Snapshot()
	if after.Projection.AgentPreset != "" || after.Projection.ModelMessages[0].Content != "DEFAULT PERSONA" {
		t.Fatalf("activation split identity and persona: %+v", after.Projection)
	}
	if !reflect.DeepEqual(before.Projection.Messages, after.Projection.Messages) {
		t.Fatal("selection rewrote canonical display history")
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := first.ReleaseSessionRuntimeBinding(); err != nil {
		t.Fatal(err)
	}
	if err := next.ReleaseSessionRuntimeBinding(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Query().Snapshot(t.Context(), ref)
	if err != nil || snapshot.Projection.AgentPreset != "" || snapshot.Projection.ModelMessages[0].Content != "DEFAULT PERSONA" {
		t.Fatalf("durable selection=%+v err=%v", snapshot.Projection, err)
	}
}

func TestAgentPresetCandidateCannotOverwriteInterveningTurn(t *testing.T) {
	service := presetService(t)
	first := presetController(t, service, nil, "", "BASE")
	if _, err := first.BindFreshSession(t.Context(), "preset-race"); err != nil {
		t.Fatal(err)
	}
	_, runtime, _ := first.SessionBinding()
	next := presetController(t, service, runtime, "review", "REVIEW")
	if err := next.AdoptRebuiltModelContext(next.agentPresetModelContext(first.History())); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Append(t.Context(), session.Batch{OperationID: "intervening", TurnID: "turn-1", Events: []session.Event{
		{Kind: "turn/start", Payload: json.RawMessage(`{"status":"in_progress"}`)},
		{Kind: "turn/end", Payload: json.RawMessage(`{"status":"completed"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	before := runtime.Session().ExecutionSnapshot()
	if err := ActivateControllerReplacement(first, next); err == nil {
		t.Fatal("stale blank selection activated after a turn")
	}
	after := runtime.Session().ExecutionSnapshot()
	if !runtime.OwnsExecution(first.ExecutionGeneration()) || after.EventSequence != before.EventSequence || !reflect.DeepEqual(before.Projection, after.Projection) {
		t.Fatal("rejected activation changed outgoing owner or projection")
	}
	if !first.AgentPresetLocked() {
		t.Fatal("completed turn did not lock preset")
	}
}

func TestOpenSessionRefreshesCustomPersonaWithoutChangingDisplayHistory(t *testing.T) {
	for _, id := range []string{"review", ""} {
		t.Run(presetLabel(id), func(t *testing.T) {
			service := presetService(t)
			first := presetController(t, service, nil, id, "OLD")
			ref, err := first.BindFreshSession(t.Context(), "restore")
			if err != nil {
				t.Fatal(err)
			}
			_, runtime, _ := first.SessionBinding()
			before := runtime.Session().Snapshot()
			if err := first.ReleaseSessionRuntimeBinding(); err != nil {
				t.Fatal(err)
			}
			next := presetController(t, service, nil, id, "LATEST")
			if _, err := next.OpenSession(t.Context(), ref); err != nil {
				t.Fatal(err)
			}
			after := runtime.Session().Snapshot()
			want := "LATEST"
			if id == "" {
				want = "OLD"
			}
			if next.History()[0].Content != want || !reflect.DeepEqual(before.Projection.Messages, after.Projection.Messages) {
				t.Fatalf("restored context/history=%+v / %+v", next.History(), after.Projection.Messages)
			}
		})
	}
}

func TestOpenSessionStagesSameIDRefreshUntilOwnedRuntimeActivation(t *testing.T) {
	service := presetService(t)
	first := presetController(t, service, nil, "review", "OLD")
	ref, err := first.BindFreshSession(t.Context(), "owned-restore")
	if err != nil {
		t.Fatal(err)
	}
	_, runtime, _ := first.SessionBinding()
	before := runtime.Session().Snapshot()
	next := presetController(t, service, nil, "review", "LATEST")
	if _, err := next.OpenSession(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if after := runtime.Session().Snapshot(); after.EventSequence != before.EventSequence || !runtime.OwnsExecution(first.ExecutionGeneration()) {
		t.Fatal("unpublished same-id attach modified live owner or context")
	}
	if err := ActivateControllerReplacement(first, next); err != nil {
		t.Fatal(err)
	}
	after := runtime.Session().Snapshot()
	if after.Projection.ModelMessages[0].Content != "LATEST" || !reflect.DeepEqual(after.Projection.Messages, before.Projection.Messages) {
		t.Fatal("same-id attach activation did not refresh context separately from display history")
	}
}

func TestOpenSessionRejectedDomainDoesNotCommitPersona(t *testing.T) {
	service := presetService(t)
	first := presetController(t, service, nil, "review", "OLD")
	ref, err := first.BindFreshSession(t.Context(), "rejected-domain")
	if err != nil {
		t.Fatal(err)
	}
	_, runtime, _ := first.SessionBinding()
	if _, err := runtime.Session().AppendBatch(t.Context(), "invalid-goal", []session.Event{{Kind: "goal/state", Payload: []byte(`{"schemaVersion":99}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := first.ReleaseSessionRuntimeBinding(); err != nil {
		t.Fatal(err)
	}
	before := runtime.Session().ExecutionSnapshot()
	next := presetController(t, service, nil, "review", "LATEST")
	if _, err := next.OpenSession(t.Context(), ref); err == nil {
		t.Fatal("unsupported goal schema was published")
	}
	after := runtime.Session().ExecutionSnapshot()
	if after.EventSequence != before.EventSequence || runtime.ExecutionBound() || !reflect.DeepEqual(before.Projection, after.Projection) {
		t.Fatal("rejected attach committed preset context or execution ownership")
	}
}

func TestAgentPresetAuthoringWithoutProviderUsesAuthoritativeRoot(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	root := t.TempDir()
	if _, err := SaveAgentPresetForRoot(root, "user", "review", "user body"); err != nil {
		t.Fatal(err)
	}
	project, err := SaveAgentPresetForRoot(root, "project", "review", "---\ntools: []\n---\nproject body")
	if err != nil || project.Scope != agentpreset.ScopeProject || project.Path != filepath.Join(root, ".deepzora", "presets", "review.md") {
		t.Fatalf("project save=%+v err=%v", project, err)
	}
	list, err := ListAgentPresetsForRoot(root)
	if err != nil || list.Current != "default" || list.Locked || len(list.Presets) != 2 || list.Presets[1].Body != "project body" || list.Presets[1].Tools == nil {
		t.Fatalf("root list=%+v err=%v", list, err)
	}
	if _, err := SaveAgentPresetForRoot(root, "other", "review", "bad"); err == nil {
		t.Fatal("invalid scope accepted")
	}
	if _, err := SaveAgentPresetForRoot("", "project", "review", "bad"); err == nil {
		t.Fatal("project authoring accepted missing root")
	}
	if err := DeleteAgentPresetForRoot(root, "project", "default"); !errors.Is(err, agentpreset.ErrBuiltin) {
		t.Fatalf("default delete=%v", err)
	}
	if err := DeleteAgentPresetForRoot(root, "project", "review"); err != nil {
		t.Fatal(err)
	}
	list, _ = ListAgentPresetsForRoot(root)
	if list.Presets[1].Body != "user body" {
		t.Fatal("deleting project definition removed user definition")
	}
}
