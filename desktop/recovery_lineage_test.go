package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/sessioncatalog"
)

func TestGetRecoveryLineageEmptyMembersEncodeAsArray(t *testing.T) {
	view := NewApp().GetRecoveryLineage(ProjectTopicKey{Scope: "global", TopicID: "missing"})
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || string(data) == "null" || view.Members == nil {
		t.Fatalf("empty lineage must keep members as []: %s", data)
	}
}

func TestChooseRecoveryBranchPersistsPreferenceAndRefreshesOffBarrier(t *testing.T) {
	isolateDesktopUserDirs(t)
	ctx := context.Background()
	dir := t.TempDir()
	catalog, err := sessioncatalog.Open(ctx, sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	root := filepath.Join(dir, "root.jsonl")
	fork := filepath.Join(dir, "fork.jsonl")
	save := func(path string, messages ...string) {
		t.Helper()
		session := agent.NewSession("system")
		for index, message := range messages {
			role := provider.RoleUser
			if index%2 == 1 {
				role = provider.RoleAssistant
			}
			session.Add(provider.Message{Role: role, Content: message})
		}
		if err := session.Save(path); err != nil {
			t.Fatal(err)
		}
	}
	save(root, "shared question", "shared answer", "root unique", "root answer")
	save(fork, "shared question", "shared answer", "fork unique", "fork answer")
	created := time.UnixMilli(100)
	for path, meta := range map[string]agent.BranchMeta{
		root: {ID: "root", Scope: "global", TopicID: "topic", TopicTitle: "Topic", CreatedAt: created, UpdatedAt: created},
		fork: {ID: "fork", Scope: "global", TopicID: "topic", TopicTitle: "Topic", Recovered: true, ParentID: "root", RecoveryDepth: 1, CreatedAt: created.Add(time.Second), UpdatedAt: created.Add(time.Second)},
	} {
		if err := agent.SaveBranchMetaPreserveUpdated(path, meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.ReconcileDirectory(ctx, sessioncatalog.DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	app := &App{tabs: map[string]*WorkspaceTab{}, detachedSessions: map[string]*WorkspaceTab{}}
	app.sessionCatalog.Store(catalog)
	var barrierReleased atomic.Bool
	app.projectTreeChangedHook = func() {
		if !app.runtimeRebuildMu.TryLock() {
			return
		}
		app.runtimeRebuildMu.Unlock()
		if app.sessionRemovalMu.TryLock() {
			app.sessionRemovalMu.Unlock()
			barrierReleased.Store(true)
		}
	}
	key := ProjectTopicKey{Scope: "global", TopicID: "topic", Path: fork}
	if view := app.GetRecoveryLineage(key); view.State != "diverged" {
		t.Fatalf("lineage before choice = %+v, want diverged", view)
	}

	if err := app.ChooseRecoveryBranch(RecoveryPreferenceRequest{Scope: "global", TopicID: "topic", Path: fork}); err != nil {
		t.Fatal(err)
	}
	if !barrierReleased.Load() {
		t.Fatal("catalog refresh after the choice still ran under the runtime mutation barrier")
	}
	meta, ok, err := agent.LoadBranchMeta(fork)
	if err != nil || !ok || !meta.RecoveryPreferred {
		t.Fatalf("fork meta = %+v ok=%v err=%v, want a persisted preference", meta, ok, err)
	}
	view := app.GetRecoveryLineage(key)
	if view.State != "preferred" || view.Unresolved != 0 {
		t.Fatalf("lineage after choice = %+v, want preferred", view)
	}
	for _, member := range view.Members {
		if member.Canonical != (member.Path == fork) {
			t.Fatalf("member %+v, want only the chosen fork canonical", member)
		}
	}

	err = app.ChooseRecoveryBranch(RecoveryPreferenceRequest{Scope: "global", TopicID: "topic", Path: filepath.Join(dir, "missing.jsonl")})
	if err == nil || err.Error() != "selected branch is outside the recovery lineage" {
		t.Fatalf("outside-lineage choice error = %v", err)
	}
}
