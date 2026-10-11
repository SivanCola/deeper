package builtin

import (
	"deepzora/internal/sandbox"
	"deepzora/internal/tool"
	"testing"
)

func TestHistoricalToolAliasesAreUnavailable(t *testing.T) {
	for _, name := range []string{"bash_output", "kill_shell", "wait", "complete_step", "review_report", "read_policy_receipt", "session_read_strategy_receipt"} {
		if _, ok := tool.LookupBuiltin(name); ok {
			t.Fatalf("retired tool %q registered", name)
		}
	}
	workspace := Workspace{Bash: sandbox.Spec{Shell: sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: "pwsh"}}}
	r := tool.NewRegistry()
	for _, t := range workspace.Tools() {
		r.Add(t)
	}
	for _, name := range []string{"bash", "shell", "powershell"} {
		if target, _, _ := r.ResolveCall(name); target != nil {
			t.Fatalf("old Windows shell alias %q resolved", name)
		}
	}
	if target, _, _ := r.ResolveCall("pwsh"); target == nil {
		t.Fatal("current PowerShell tool missing")
	}
}
