package boot

import (
	"deepzora/internal/agent"
	"deepzora/internal/tool"
)

func registerInteractiveAgentTools(reg *tool.Registry) {
	reg.Add(agent.NewAskTool())
}
