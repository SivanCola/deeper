package main

import (
	"testing"

	"deepzora/internal/sessioncatalog"
)

func TestSessionMetaFromCatalogPropagatesRecoveryCopy(t *testing.T) {
	meta := sessionMetaFromCatalog(sessioncatalog.SessionRecord{
		Path: "/s/copy.jsonl", Preview: "hello", Turns: 2, TurnsState: sessioncatalog.TurnsValid,
		Recovered: true, RecoveryCopy: true, LastActivityAt: 42,
	}, false, false)
	if !meta.Recovered || !meta.RecoveryCopy {
		t.Fatalf("meta = %+v, want recovered recoveryCopy", meta)
	}
	meta = sessionMetaFromCatalog(sessioncatalog.SessionRecord{
		Path: "/s/adopted.jsonl", Recovered: true, RecoveryCopy: false,
	}, false, false)
	if !meta.Recovered || meta.RecoveryCopy {
		t.Fatalf("meta = %+v, want recovered without recoveryCopy", meta)
	}
}

func TestCanonicalSidebarKeepsSharedTopicSessionsIndependent(t *testing.T) {
	app, root, refs := canonicalOrganizationFixtureWithTopic(t, "shared", "a", "b")
	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 10})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("independent rows: %+v %v", page, err)
	}
	found := map[string]bool{}
	for _, node := range page.Items {
		if node.Session == nil || refs[node.Session.SessionID] != *node.Session {
			t.Fatalf("row lost identity: %+v", node)
		}
		found[node.Session.SessionID] = true
	}
	if len(found) != 2 || page.Items[0].Key == page.Items[1].Key {
		t.Fatal("shared topic collapsed independent sessions")
	}
}
