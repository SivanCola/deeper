package sessiontool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func currentTitleTool(t *testing.T, service *session.Service, ref session.SessionRef) *setSessionTitleTool {
	t.Helper()
	return NewSetSessionTitleEventTool(func() string { return ref.SessionID }, func(ctx context.Context, title string) error {
		runtime, ok := service.Runtime(ref)
		if !ok {
			return errors.New("current runtime is unavailable")
		}
		payload, err := json.Marshal(map[string]string{"title": title})
		if err != nil {
			return err
		}
		_, err = runtime.Session().Append(ctx, session.Batch{OperationID: "title-" + title, Events: []session.Event{{Kind: "session/title", Payload: payload}}})
		return err
	})
}

func TestSetSessionTitleUsesHostBoundCurrentSession(t *testing.T) {
	service := currentSessionService(t)
	current := writeCurrentSession(t, service, "current", []provider.Message{{Role: provider.RoleUser, Content: "hello"}})
	other := writeCurrentSession(t, service, "other", []provider.Message{{Role: provider.RoleUser, Content: "other"}})
	tool := currentTitleTool(t, service, current)
	got, err := tool.Execute(t.Context(), json.RawMessage(`{"title":"  Current work  ","session":"other"}`))
	if err != nil || !strings.Contains(got, "Current work") {
		t.Fatalf("Execute: %q, %v", got, err)
	}
	info, err := service.Query().Stat(t.Context(), current)
	if err != nil || info.Title != "Current work" {
		t.Fatalf("current title: %+v, %v", info, err)
	}
	info, err = service.Query().Stat(t.Context(), other)
	if err != nil || info.Title != "" {
		t.Fatalf("other session changed: %+v, %v", info, err)
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"title":""}`)); err != nil {
		t.Fatal(err)
	}
	info, err = service.Query().Stat(t.Context(), current)
	if err != nil || info.Title != "" {
		t.Fatalf("title was not cleared: %+v, %v", info, err)
	}
}

func TestSetSessionTitleRejectsUnavailableCurrentSession(t *testing.T) {
	for _, tool := range []*setSessionTitleTool{
		nil, NewSetSessionTitleEventTool(nil, nil),
		NewSetSessionTitleEventTool(func() string { return "" }, func(context.Context, string) error { t.Fatal("unbound writer called"); return nil }),
		NewSetSessionTitleEventTool(func() string { return "current" }, nil),
	} {
		if _, err := tool.Execute(t.Context(), json.RawMessage(`{"title":"Title"}`)); err == nil {
			t.Fatal("missing session accepted")
		}
	}
}

func TestSetSessionTitlePropagatesWriterFailure(t *testing.T) {
	failure := errors.New("writer refused")
	tool := NewSetSessionTitleEventTool(func() string { return "current" }, func(context.Context, string) error { return failure })
	if out, err := tool.Execute(t.Context(), json.RawMessage(`{"title":"Title"}`)); !errors.Is(err, failure) || out != "" {
		t.Fatalf("writer failure: %q, %v", out, err)
	}
}

func TestSetSessionTitleRequiresTitleAndRejectsSubagent(t *testing.T) {
	tool := NewSetSessionTitleEventTool(func() string { return "current" }, func(context.Context, string) error { t.Fatal("invalid request reached writer"); return nil })
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing title accepted")
	}
	tooLong, _ := json.Marshal(map[string]string{"title": strings.Repeat("界", setSessionTitleMaxRunes+1)})
	if _, err := tool.Execute(t.Context(), tooLong); err == nil {
		t.Fatal("oversized title accepted")
	}
	if _, err := tool.Execute(agent.WithSubagentDepth(t.Context(), 1), json.RawMessage(`{"title":"Wrong owner"}`)); err == nil {
		t.Fatal("subagent title accepted")
	}
}
