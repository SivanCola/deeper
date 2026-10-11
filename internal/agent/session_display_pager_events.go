package agent

import "deepzora/internal/provider"

// EventMessages reads a bounded range from the current DAG.
func (p *DisplayPager) EventMessages(lo, hi int) ([]provider.Message, error) {
	return p.DAGMessages(lo, hi)
}
