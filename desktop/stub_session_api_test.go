package main

import (
	"deepzora/internal/agent"
	"deepzora/internal/control"
)

// stubSessionAPI supplies port defaults for the test controllers that only
// override the few methods their scenario exercises. Embedding the bare
// control.SessionAPI interface leaves it nil, so every un-overridden call
// panics; production readers call the port directly and must not carry a
// recover to absorb that.
type stubSessionAPI struct {
	control.SessionAPI
}

func (stubSessionAPI) SessionHead() (agent.HeadRef, bool) { return agent.HeadRef{}, false }
