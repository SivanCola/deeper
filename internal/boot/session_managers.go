package boot

import (
	"deepzora/internal/persistentshell"
	"deepzora/internal/sessiontemp"
	"deepzora/internal/tool"
	"deepzora/internal/tool/builtin"
)

func sessionManagers(opts Options) (*sessiontemp.Manager, *persistentshell.Manager) {
	st := opts.SessionTemp
	if st == nil {
		st = sessiontemp.New()
	}
	return st, persistentshell.OrNew(opts.PersistentShell)
}

func bindPersistentShell(reg *tool.Registry, m *persistentshell.Manager) {
	if reg == nil || m == nil {
		return
	}
	t, ok := reg.Get("bash")
	if !ok {
		return
	}
	rebound, ok := builtin.BindPersistentShell(t, m)
	if ok {
		reg.Add(rebound)
	}
}
