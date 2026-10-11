package boot

import (
	"deepzora/internal/agent"
	"deepzora/internal/history"
)

func newObservedSession(systemPrompt string) *agent.Session {
	session := agent.NewSession(systemPrompt)
	session.SetPersistObserver(history.PersistObserver())
	return session
}
