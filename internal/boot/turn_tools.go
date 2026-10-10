package boot

import (
	"deeper/internal/agent"
	"deeper/internal/tool"
)

func registerInteractiveAgentTools(reg *tool.Registry) {
	reg.Add(agent.NewAskTool())
}
