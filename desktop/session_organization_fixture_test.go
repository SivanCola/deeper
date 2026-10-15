package main

import (
	"path/filepath"
	"testing"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

func canonicalOrganizationFixture(t *testing.T, ids ...string) (*App, string, map[string]session.SessionRef) {
	t.Helper()
	return canonicalOrganizationFixtureWithTopic(t, "", ids...)
}

func setSessionOrderForTest(t *testing.T, app *App, scope, root string, keys []string) error {
	t.Helper()
	id, _, err := app.ensureSessionOrganization(scope, root)
	if err != nil {
		return err
	}
	_, _, err = app.workspaceRegistry().UpdateOrganization(t.Context(), id, nil, func(o *workspacestate.Organization) error {
		o.Order = append([]string{}, keys...)
		o.ManualOrderEnabled = true
		return nil
	})
	return err
}

func setSessionGroupsForTest(t *testing.T, app *App, scope, root string, groups []desktopGroup) error {
	t.Helper()
	id, _, err := app.ensureSessionOrganization(scope, root)
	if err != nil {
		return err
	}
	_, _, err = app.workspaceRegistry().UpdateOrganization(t.Context(), id, nil, func(o *workspacestate.Organization) error {
		next := make([]workspacestate.OrganizationGroup, 0, len(groups))
		for _, group := range groups {
			members := append([]string{}, group.SessionKeys...)
			next = append(next, workspacestate.OrganizationGroup{ID: group.ID, Title: group.Title, Members: members})
		}
		o.Groups = next
		return nil
	})
	return err
}

func canonicalOrganizationFixtureWithTopic(t *testing.T, sharedTopic string, ids ...string) (*App, string, map[string]session.SessionRef) {
	t.Helper()
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Organization test"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	app.ctx = t.Context()
	app.desktopSessions.root = filepath.Join(root, "by-id")
	app.desktopSessions.workspaceState = workspacestate.NewStore(filepath.Join(root, "workspace-state.json"))
	workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "project", root)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]session.SessionRef{}
	for _, id := range ids {
		runtime, err := app.desktopSessionService("").Create(t.Context(), session.CreateOptions{SessionID: id, CWD: root, Origin: session.SessionOriginNew})
		if err != nil {
			t.Fatal(err)
		}
		if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspaceID, id, ""); err != nil {
			t.Fatal(err)
		}
		topic := id
		if sharedTopic != "" {
			topic = sharedTopic
		}
		if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), id, topic, id); err != nil {
			t.Fatal(err)
		}
		refs[id] = runtime.Ref()
	}
	return app, root, refs
}
