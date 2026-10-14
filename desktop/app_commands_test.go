package main

import (
	"errors"
	"strings"
	"testing"

	"deepzora/internal/command"
	"deepzora/internal/control"
	"deepzora/internal/skill"
)

func TestCommandsDocsShowsOnlyRuntimeWinner(t *testing.T) {
	tests := []struct {
		name     string
		commands []command.Command
		skills   []skill.Skill
		wantKind string
	}{
		{
			name:     "custom command shadows builtin",
			commands: []command.Command{{Name: "docs", Description: "custom docs"}},
			wantKind: "custom",
		},
		{
			name:     "skill shadows builtin",
			skills:   []skill.Skill{{Name: "docs", Description: "docs skill"}},
			wantKind: "skill",
		},
		{
			name:     "custom command shadows skill and builtin",
			commands: []command.Command{{Name: "docs", Description: "custom docs"}},
			skills:   []skill.Skill{{Name: "docs", Description: "docs skill"}},
			wantKind: "custom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := control.New(control.Options{Commands: tt.commands, Skills: tt.skills})
			defer ctrl.Close()
			app := NewApp()
			app.setTestCtrl(ctrl, "")

			var docs []CommandInfo
			for _, cmd := range app.Commands() {
				if cmd.Name == "docs" {
					docs = append(docs, cmd)
				}
			}
			if len(docs) != 1 || docs[0].Kind != tt.wantKind {
				t.Fatalf("docs commands = %+v, want one %s entry", docs, tt.wantKind)
			}
			if fallback, ok := commandInfoByName(app.Commands(), control.DeepzoraDocsSlashName); !ok || fallback.Kind != "builtin" {
				t.Fatalf("qualified docs fallback = %+v, %v; want built-in", fallback, ok)
			}
		})
	}
}

func TestCommandsDocsDoesNotDisplaceQualifiedCustomCommands(t *testing.T) {
	ctrl := control.New(control.Options{Commands: []command.Command{
		{Name: "docs", Description: "custom docs"},
		{Name: "deepzora:docs", Description: "qualified custom docs"},
		{Name: "deepzora:builtin:docs", Description: "second qualified custom docs"},
	}})
	defer ctrl.Close()
	app := NewApp()
	app.setTestCtrl(ctrl, "")
	commands := app.Commands()
	for _, want := range []struct {
		name string
		kind string
	}{
		{name: "docs", kind: "custom"},
		{name: "deepzora:docs", kind: "custom"},
		{name: "deepzora:builtin:docs", kind: "custom"},
		{name: "deepzora:builtin:docs:2", kind: "builtin"},
	} {
		if command, ok := commandInfoByName(commands, want.name); !ok || command.Kind != want.kind {
			t.Fatalf("command %q = %+v, %v; want kind %q", want.name, command, ok, want.kind)
		}
	}
}

func TestCommandsClassifiesSubagentSkills(t *testing.T) {
	ctrl := control.New(control.Options{Skills: []skill.Skill{
		{Name: "init", Description: "inline skill", RunAs: skill.RunInline},
		{Name: "explore", Description: "isolated skill", RunAs: skill.RunSubagent, Color: "amber"},
	}})
	defer ctrl.Close()
	app := NewApp()
	app.setTestCtrl(ctrl, "")

	kinds := map[string]string{}
	groups := map[string]string{}
	colors := map[string]string{}
	for _, cmd := range app.Commands() {
		kinds[cmd.Name] = cmd.Kind
		groups[cmd.Name] = cmd.Group
		colors[cmd.Name] = cmd.Color
	}
	if kinds["init"] != "skill" {
		t.Fatalf("inline skill kind = %q, want skill", kinds["init"])
	}
	if kinds["explore"] != "subagent" {
		t.Fatalf("subagent skill kind = %q, want subagent", kinds["explore"])
	}
	if colors["explore"] != "amber" {
		t.Fatalf("subagent skill color = %q, want amber", colors["explore"])
	}
	if groups["new"] != "actions" {
		t.Fatalf("new command group = %q, want actions", groups["new"])
	}
	if groups["mcp"] != "integrations" || groups["plugins"] != "integrations" {
		t.Fatalf("integration command groups = mcp:%q plugins:%q", groups["mcp"], groups["plugins"])
	}
	if groups["skill"] != "skills" {
		t.Fatalf("skill command group = %q, want skills", groups["skill"])
	}
}

func TestUserTriggeredCommandsReturnErrorsWhenUnavailable(t *testing.T) {
	tests := []struct {
		name string
		app  *App
		call func(*App) error
		want string
	}{
		{
			name: "submit read-only",
			app: &App{
				tabs:        map[string]*WorkspaceTab{"test": {ID: "test", Scope: "global", ReadOnly: true}},
				activeTabID: "test",
			},
			call: func(app *App) error {
				return app.SubmitToTabWithID("test", "hello", newSessionRuntimeID("test-submit"))
			},
			want: "read-only",
		},
		{
			name: "submit workspace unavailable",
			app: &App{
				tabs:        map[string]*WorkspaceTab{"test": {ID: "test", Scope: "global", StartupErr: "boom"}},
				activeTabID: "test",
			},
			call: func(app *App) error {
				return app.SubmitToTabWithID("test", "hello", newSessionRuntimeID("test-submit"))
			},
			want: "workspace failed to start: boom",
		},
		{
			name: "run shell workspace unavailable",
			app: &App{
				tabs:        map[string]*WorkspaceTab{"test": {ID: "test", Scope: "global"}},
				activeTabID: "test",
			},
			call: func(app *App) error { return app.RunShellForTab("test", "echo hi") },
			want: "workspace is still starting",
		},
		{
			name: "steer workspace unavailable",
			app: &App{
				tabs:        map[string]*WorkspaceTab{"test": {ID: "test", Scope: "global"}},
				activeTabID: "test",
			},
			call: func(app *App) error { return app.SteerForTab("test", "please continue") },
			want: "workspace is still starting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(tt.app)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want to contain %q", err, tt.want)
			}
		})
	}
}

func TestInvocationEntryPointsAllowEmptyExplicitTaskForSkillOnlyTurn(t *testing.T) {
	invocations := []InvocationRequest{{Name: "skill", Kind: "skill"}}
	if err := validateInvocationTurnInput("", invocations); err != nil {
		t.Fatalf("skill-only invocation input rejected: %v", err)
	}
	if err := validateInvocationTurnInput("", nil); !errors.Is(err, errEmptyTurnInput) {
		t.Fatalf("empty input without invocations = %v, want errEmptyTurnInput", err)
	}
}
