package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func currentBranchController(t *testing.T, scope func(context.Context) (map[string]bool, error)) (*Controller, *session.Service) {
	t.Helper()
	prov := &recordingProvider{name: "executor", streams: [][]provider.Chunk{textTurn("root answer"), textTurn("child answer")}}
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	return bindCurrentBranchController(t, Options{Runner: exec, Executor: exec, SystemPrompt: "sys", SessionReadScope: scope})
}

func bindCurrentBranchController(t *testing.T, opts Options) (*Controller, *session.Service) {
	t.Helper()
	service, err := session.NewService("branch-test", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	opts.SessionService, opts.ExclusiveSession = service, true
	opts.SessionDir = ""
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown branch service: %v", err)
		}
	})
	c := newOwnedTestController(t, opts)
	if _, err := c.BindFreshSession(t.Context(), "root-current"); err != nil {
		t.Fatal(err)
	}
	return c, service
}

func createColdBranchSession(t *testing.T, service *session.Service, id, title string) session.SessionRef {
	t.Helper()
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"message": provider.Message{ID: "message-" + id, Role: provider.RoleUser, Content: id + " content"}})
	if _, err := runtime.Session().AppendBatch(t.Context(), "seed", []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetTitle(t.Context(), runtime.Ref(), title); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	ref := runtime.Ref()
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestCurrentBranchesCreateSwitchAndKeepIndependentHistory(t *testing.T) {
	c, service := currentBranchController(t, nil)
	if err := c.RunTurn(t.Context(), "root prompt"); err != nil {
		t.Fatal(err)
	}
	parent, _ := c.SessionRef()
	before, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	childID, err := c.Branch("experiment")
	if err != nil {
		t.Fatal(err)
	}
	if c.CurrentBranchID() != childID || childID == parent.SessionID {
		t.Fatalf("branch identity = %q, child %q, path %q", c.CurrentBranchID(), childID, c.SessionRoute())
	}
	if err := c.RunTurn(t.Context(), "child prompt"); err != nil {
		t.Fatal(err)
	}
	branches, err := c.Branches()
	if err != nil || len(branches) != 2 {
		t.Fatalf("Branches = %+v, %v", branches, err)
	}
	for _, branch := range branches {
		if branch.ID == childID && (branch.ParentID != parent.SessionID || branch.Name != "experiment") {
			t.Fatalf("child lineage = %+v", branch)
		}
	}
	if _, err := c.SwitchBranch("root-cur"); err != nil {
		t.Fatal(err)
	}
	if c.CurrentBranchID() != parent.SessionID || !reflect.DeepEqual(c.History(), before.Projection.ModelMessages) {
		t.Fatal("switch did not restore the unchanged parent")
	}
	after, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil || after.EventSequence != before.EventSequence || !reflect.DeepEqual(after.Projection.Messages, before.Projection.Messages) {
		t.Fatalf("branch changed parent events: %v", err)
	}
	if _, err := c.SwitchBranch("experiment"); err != nil {
		t.Fatal(err)
	}
	if c.CurrentBranchID() != childID || !strings.Contains(requestMessagesText(c.History()), "child prompt") {
		t.Fatalf("name switch lost child %q: current %q, history %+v", childID, c.CurrentBranchID(), c.History())
	}
	tree := c.BranchTreeText()
	marked := false
	for line := range strings.SplitSeq(tree, "\n") {
		marked = marked || (strings.Contains(line, "experiment") && strings.HasSuffix(line, "  current"))
	}
	if !marked {
		t.Fatalf("branch tree did not mark child current: %s", tree)
	}
}

func TestCurrentBranchRetainsPostTurnModelContext(t *testing.T) {
	c, service := currentBranchController(t, nil)
	if err := c.RunTurn(t.Context(), "root prompt"); err != nil {
		t.Fatal(err)
	}
	parent, _ := c.SessionRef()
	contextMessages := []provider.Message{{ID: "summary", Role: provider.RoleSystem, Content: "compressed root context"}}
	result, err := c.RecordSessionModelContext(t.Context(), agent.SessionModelContextCommit{OperationID: "compact-root", Reason: "summary", Messages: contextMessages})
	if err != nil || !result.Durable {
		t.Fatalf("context commit = %+v, %v", result, err)
	}
	before, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	childID, err := c.Branch("compacted branch")
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.Query().Snapshot(t.Context(), session.SessionRef{HostID: parent.HostID, SessionID: childID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(child.Projection.ModelMessages, before.Projection.ModelMessages) || !reflect.DeepEqual(child.Projection.Messages, before.Projection.Messages) {
		t.Fatal("branch lost post-turn model context or display history")
	}
	if !reflect.DeepEqual(c.History(), contextMessages) {
		t.Fatal("branch executor restored the uncompressed context")
	}
}

func TestCurrentBranchesReadAllPagesWithoutOpeningWriters(t *testing.T) {
	allowed := map[string]bool{}
	c, service := currentBranchController(t, func(context.Context) (map[string]bool, error) { return allowed, nil })
	refs := make([]session.SessionRef, 0, 101)
	for i := range 101 {
		id := fmt.Sprintf("saved-%03d", i)
		refs = append(refs, createColdBranchSession(t, service, id, id))
		allowed[id] = true
	}
	foreign := createColdBranchSession(t, service, "foreign", "other workspace")
	rows, err := c.Branches()
	if err != nil || len(rows) != 101 {
		t.Fatalf("paged branches: %d, %v", len(rows), err)
	}
	for _, ref := range append(refs, foreign) {
		if _, live := service.Runtime(ref); live {
			t.Fatalf("listing acquired %s writer", ref.SessionID)
		}
	}
	if _, err := c.SwitchBranch("foreign"); err == nil {
		t.Fatal("switched to a session outside host membership")
	}
	delete(allowed, "saved-100")
	if _, err := c.SwitchBranch("saved-100"); err == nil {
		t.Fatal("scope revocation was not observed")
	}
}

func TestCurrentBranchSwitchFailuresPreserveIdentity(t *testing.T) {
	c, service := currentBranchController(t, nil)
	first := createColdBranchSession(t, service, "first", "duplicate")
	createColdBranchSession(t, service, "second", "duplicate")
	before, _ := c.SessionRef()
	for _, selector := range []string{"duplicate", "missing", filepath.Join(t.TempDir(), "first.jsonl")} {
		if _, err := c.SwitchBranch(selector); err == nil {
			t.Fatalf("accepted invalid selector %q", selector)
		}
		if got, _ := c.SessionRef(); got != before {
			t.Fatalf("failed switch changed identity: %+v", got)
		}
	}
	c.sessionReadScope = func(context.Context) (map[string]bool, error) { return nil, errors.New("registry unavailable") }
	if _, err := c.SwitchBranch("first"); err == nil {
		t.Fatal("scope read failure was ignored")
	}
	if _, live := service.Runtime(first); live {
		t.Fatal("failed selection acquired the target writer")
	}
}

func TestCurrentBranchSwitchRechecksScopeBeforeAttachment(t *testing.T) {
	reads := 0
	c, service := currentBranchController(t, func(context.Context) (map[string]bool, error) {
		reads++
		return map[string]bool{"target": reads == 1}, nil
	})
	target := createColdBranchSession(t, service, "target", "target")
	before, _ := c.SessionRef()
	if _, err := c.SwitchBranch("target"); err == nil || !strings.Contains(err.Error(), "no longer in the current scope") {
		t.Fatalf("scope change was not rejected: %v", err)
	}
	if got, _ := c.SessionRef(); got != before {
		t.Fatal("scope change replaced the original binding")
	}
	if _, live := service.Runtime(target); live {
		t.Fatal("scope change acquired the target writer")
	}
}
