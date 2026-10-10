package boot

import (
	"deeper/internal/sandbox"
	"deeper/internal/tool"
	"deeper/internal/tool/builtin"
)

func canonicalBuiltinName(name string) string {
	if tool.IsShellToolName(name) {
		return "bash"
	}
	return name
}

func registerShellBuiltin(reg *tool.Registry, shell tool.Tool, writeRootSet *sandbox.WritableRootSet) {
	if _, ok := reg.Get("bash"); !ok {
		return
	}
	shell = builtin.BindWriteRootSet(shell, writeRootSet)
	reg.Add(shell)
	if shell.Name() == "pwsh" {
		reg.Remove("bash")
	}
}
