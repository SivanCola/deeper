package boot

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/agent/testutil"
	"deepzora/internal/agentpreset"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/sessioncontext"
)

func TestEffectCompleteAgentPresetOwnsFinalPrompt(t *testing.T) {
	opts, rec := presetEffectSetup(t)
	configPath := opts.WorkspaceRoot + "/deepzora.toml"
	configText, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, opts.WorkspaceRoot, "deepzora.toml", strings.Replace(string(configText), `system_prompt = "BASE CODING"`, "system_prompt = \"BASE CODING\"\noutput_style = \"concise\"", 1))
	approveWorkspace(t, opts.WorkspaceRoot)
	writeFile(t, opts.WorkspaceRoot, "DEEPZORA.md", "STANDING INSTRUCTION THAT MUST NOT ENTER COMPLETE PROMPT")
	writeFile(t, opts.WorkspaceRoot, ".deepzora/skills/complete-test/SKILL.md", "---\ndescription: OPTIONAL SKILL CATALOG\n---\nExplicit skill body")
	const body = "You are a travel writer.\nDescribe places without a coding persona."
	if _, err := agentpreset.Write(AgentPresetDirs(opts.WorkspaceRoot).Project, "writer", "---\ncomplete: true\ninclude-runtime-context: false\ntools: []\n---\n"+body); err != nil {
		t.Fatal(err)
	}
	installBootFakePlugin(t, config.DeepzoraHomeDir(), "prompt-owner", map[string]any{
		"replaces": []string{"system_prompt"},
		"env":      map[string]string{bootFakeEnvReplacePrompt: "EXTENSION PROMPT MUST NOT WIN"},
	})
	opts.AgentPreset = "writer"
	result := buildPresetEffect(t, opts)
	if result.Dispatcher == nil || result.Snapshot == nil {
		t.Fatal("complete preset test did not assemble the real extension strategy")
	}
	if result.Snapshot.SystemPrompt() != body || result.Controller.SystemPrompt() != body || systemMessage(result.Controller.History()) != body {
		t.Fatal("snapshot, controller and executor do not share the complete prompt")
	}
	for _, input := range []string{"First place", "Second place"} {
		if err := result.Controller.Run(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	requests := rec.requests()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d", len(requests))
	}
	for _, request := range requests {
		if got := systemMessage(request.Messages); got != body {
			t.Fatalf("complete provider prompt=%q", got)
		}
		foundContext := false
		for _, message := range request.Messages {
			if snapshot, ok := sessioncontext.Parse(message.Content); ok {
				foundContext = true
				if snapshot.Sections.Workspace != currentWorkspacePromptLine(opts.WorkspaceRoot) || snapshot.Sections.Environment != "" || snapshot.Sections.BackgroundMemory != "" || snapshot.Sections.SkillsCatalog != "" {
					t.Fatalf("complete cwd-only context=%+v", snapshot.Sections)
				}
			}
		}
		if !foundContext {
			t.Fatal("required working directory context was removed")
		}
		if names := toolNames(request); len(names) != 1 || !names["use_capability"] {
			t.Fatalf("complete mode changed tool admission=%v", names)
		}
	}
	if !reflect.DeepEqual(requests[0].Messages[0], requests[1].Messages[0]) || !reflect.DeepEqual(requests[0].Tools, requests[1].Tools) {
		t.Fatal("complete prefix or tools changed across turns")
	}
	if strings.Contains(requests[0].Messages[0].Content, "STANDING INSTRUCTION") {
		t.Fatal("standing instructions escaped complete mode")
	}
}

func TestCompleteAgentPresetSuppressesOfficialModelPersona(t *testing.T) {
	isolateConfigHome(t)
	root := robustTempDir(t)
	const key = "BOOT_COMPLETE_V4_FIXTURE_KEY"
	t.Setenv(key, "disposable-test-key")
	writeFile(t, root, "deepzora.toml", `
default_model = "deepseek/deepseek-v4-pro"
[agent]
system_prompt = "BASE CODING"
[environment]
enabled = false
[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-pro", "deepseek-v4-flash"]
api_key_env = "`+key+`"
`)
	approveWorkspace(t, root)
	// Build only: no network requests are made to the official endpoint.
	// The exact official-persona prefix is author text and must not be stripped.
	body := config.OfficialDeepSeekV4ProPersona + "\nThis text belongs to the preset."
	if _, err := agentpreset.Write(AgentPresetDirs(root).Project, "complete", "---\ncomplete: true\n---\n"+body); err != nil {
		t.Fatal(err)
	}
	opts := withTestSession(t, Options{WorkspaceRoot: root, AgentPreset: "complete", Sink: event.Discard})
	first := buildPresetEffect(t, opts)
	if first.Controller.SystemPrompt() != body || first.Snapshot.SystemPrompt() != body {
		t.Fatal("official model rewrote the complete preset")
	}
	opts.Model = "deepseek/deepseek-v4-flash"
	second, err := Rebuild(t.Context(), first.Controller, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Controller.Close)
	if second.Controller.SystemPrompt() != body || second.Snapshot.SystemPrompt() != body {
		t.Fatal("switching off the official model stripped preset-owned text")
	}
}

func TestEffectAgentPresetFlagsRebuildIndependently(t *testing.T) {
	opts, rec := presetEffectSetup(t)
	writeFile(t, opts.WorkspaceRoot, ".deepzora/skills/visible/SKILL.md", "---\ndescription: OPTIONAL CATALOG SENTINEL\n---\nSkill body")
	dir := AgentPresetDirs(opts.WorkspaceRoot).Project
	const body = "A stable author-owned persona."
	opts.AgentPreset = "flags"
	var previous *BuildResult
	for _, flags := range []struct{ complete, include bool }{{false, true}, {true, true}, {true, false}, {false, false}} {
		content := fmt.Sprintf("---\ncomplete: %t\ninclude-runtime-context: %t\ntools: []\n---\n%s", flags.complete, flags.include, body)
		if _, err := agentpreset.Write(dir, "flags", content); err != nil {
			t.Fatal(err)
		}
		current := previous
		if previous == nil {
			current = buildPresetEffect(t, opts)
		} else {
			var err error
			current, err = RebuildFrom(t.Context(), previous, Options{WorkspaceRoot: opts.WorkspaceRoot, Sink: event.Discard})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(current.Controller.Close)
			if current.ReusedController || current.Controller == previous.Controller {
				t.Fatalf("flag-only edit reused stale controller: %+v", flags)
			}
			if err := control.ActivateControllerReplacement(previous.Controller, current.Controller); err != nil {
				t.Fatal(err)
			}
		}
		if err := current.Controller.Run(t.Context(), "Check current flags"); err != nil {
			t.Fatal(err)
		}
		requests := rec.requests()
		request := requests[len(requests)-1]
		prompt := systemMessage(request.Messages)
		if flags.complete {
			if prompt != body || current.Snapshot.SystemPrompt() != body {
				t.Fatalf("complete prompt=%q", prompt)
			}
		} else if !strings.Contains(prompt, "BASE CODING") || !strings.Contains(prompt, body) {
			t.Fatalf("ordinary prompt=%q", prompt)
		}
		var latest sessioncontext.Snapshot
		for _, message := range request.Messages {
			if snapshot, ok := sessioncontext.Parse(message.Content); ok {
				latest = snapshot
			}
		}
		if latest.Sections.Workspace != currentWorkspacePromptLine(opts.WorkspaceRoot) || (latest.Sections.SkillsCatalog != "") != flags.include {
			t.Fatalf("flags=%+v latest runtime sections=%+v", flags, latest.Sections)
		}
		if current.Controller.AgentPreset() != "flags" || !current.Controller.AgentPresetLocked() {
			t.Fatal("flag-only rebuild lost preset identity or first-turn lock")
		}
		previous = current
	}
}

func TestEffectCompleteAgentPresetRetainsPermissionDenial(t *testing.T) {
	opts, _ := presetEffectSetup(t)
	const body = "Only this system text is visible."
	if _, err := agentpreset.Write(AgentPresetDirs(opts.WorkspaceRoot).Project, "denied", "---\ncomplete: true\ninclude-runtime-context: false\ntools: [write_file]\n---\n"+body); err != nil {
		t.Fatal(err)
	}
	writeUserConfig(t, "[permissions]\ndeny = [\"write_file\"]\n")
	target := opts.WorkspaceRoot + "/denied.txt"
	mock := testutil.NewMock("complete-denial", testutil.Turn{ToolCalls: []provider.ToolCall{{
		ID: "denied", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"must not be written"}`, target),
	}}}, testutil.Turn{Text: "done"})
	const kind = "complete-permission-denial"
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return mock, nil })
	writeFile(t, opts.WorkspaceRoot, "deepzora.toml", "default_model = \"test-model\"\n[agent]\nsystem_prompt = \"BASE CODING\"\n[environment]\nenabled = false\n[[providers]]\nname = \"test-model\"\nkind = \""+kind+"\"\nmodels = [\"x\"]\n")
	approveWorkspace(t, opts.WorkspaceRoot)
	opts.AgentPreset, opts.Model = "denied", "test-model"
	result := buildPresetEffect(t, opts)
	result.Controller.ApplyHeadlessApprovalMode(control.ToolApprovalDangerFullAccess)
	if err := result.Controller.Run(t.Context(), "Try the denied write"); err != nil {
		t.Fatalf("run denied tool: %v", err)
	}
	requests := mock.Requests()
	if len(requests) == 0 || systemMessage(requests[0].Messages) != body {
		t.Fatalf("complete prompt did not reach provider: requests=%+v", requests)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("denied file exists: %v", err)
	}
	for _, message := range result.Controller.History() {
		if message.Role == provider.RoleTool && message.ToolCallID == "denied" && strings.Contains(message.Content, "denied by permission policy") {
			return
		}
	}
	t.Fatal("no runtime permission denial recorded for the admitted tool")
}
