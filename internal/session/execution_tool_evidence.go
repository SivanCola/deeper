package session

import (
	"encoding/json"
	"maps"
	"slices"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
)

// Execution evidence is retained separately from the provider wire projection.
// It contains no message bodies and survives a model-only context replacement.
type toolCallEvidence struct {
	ID           string
	Name         string
	Recovery     *provider.ToolCallRecord
	WriteIntents []json.RawMessage
}

func noteToolCallEvidence(p *Projection, message provider.Message) {
	if message.ID == "" {
		return
	}
	var records []toolCallEvidence
	for _, call := range message.ToolCalls {
		if call.Recovery != nil || len(call.WriteIntents) > 0 {
			records = append(records, toolCallEvidence{ID: call.ID, Name: call.Name, Recovery: call.Recovery, WriteIntents: call.WriteIntents})
		}
	}
	if len(records) == 0 {
		delete(p.ToolCallEvidence, message.ID)
		return
	}
	if p.ToolCallEvidence == nil {
		p.ToolCallEvidence = map[string][]toolCallEvidence{}
	}
	p.ToolCallEvidence[message.ID] = records
}

func evidenceMessages(id string, records []toolCallEvidence) []provider.Message {
	calls := make([]provider.ToolCall, 0, len(records))
	for _, record := range records {
		calls = append(calls, provider.ToolCall{ID: record.ID, Name: record.Name, Recovery: record.Recovery, WriteIntents: record.WriteIntents})
	}
	retained := agent.PlanRetainedToolRecords([]provider.Message{{ID: id, ToolCalls: calls}}, nil)
	for i := range retained {
		retained[i].ID = "tool-evidence:" + id + ":" + retained[i].ToolCalls[0].ID
	}
	return retained
}

func pruneToolCallEvidence(p *Projection) {
	if len(p.ToolCallEvidence) == 0 {
		return
	}
	current := make(map[string]bool, len(p.ModelMessages))
	for _, message := range p.ModelMessages {
		current[message.ID] = true
	}
	for id, records := range p.ToolCallEvidence {
		if current[id] {
			continue
		}
		retained := evidenceMessages(id, records)
		if len(retained) == 0 {
			delete(p.ToolCallEvidence, id)
			continue
		}
		calls := map[string]bool{}
		for _, message := range retained {
			calls[message.ToolCalls[0].ID] = true
		}
		kept := make([]toolCallEvidence, 0, len(retained))
		for _, record := range records {
			if calls[record.ID] {
				kept = append(kept, record)
			}
		}
		p.ToolCallEvidence[id] = kept
	}
}

func detachToolCallEvidence(p *Projection) {
	for id, records := range p.ToolCallEvidence {
		copy := append([]toolCallEvidence(nil), records...)
		for i := range copy {
			if copy[i].Recovery != nil {
				r := *copy[i].Recovery
				r.Arguments = cloneRaw(r.Arguments)
				copy[i].Recovery = &r
			}
			copy[i].WriteIntents = append([]json.RawMessage(nil), copy[i].WriteIntents...)
			for j := range copy[i].WriteIntents {
				copy[i].WriteIntents[j] = cloneRaw(copy[i].WriteIntents[j])
			}
		}
		p.ToolCallEvidence[id] = copy
	}
}

func restoreExecutionToolEvidence(p *Projection) {
	if len(p.ToolCallEvidence) == 0 {
		return
	}
	present := map[string]bool{}
	attempts := map[string]bool{}
	for i := range p.ModelMessages {
		message := &p.ModelMessages[i]
		present[message.ID] = true
		for _, record := range p.ToolCallEvidence[message.ID] {
			for j := range message.ToolCalls {
				call := &message.ToolCalls[j]
				if call.ID == record.ID && call.Name == record.Name {
					call.Recovery, call.WriteIntents = record.Recovery, record.WriteIntents
				}
			}
		}
		for _, call := range message.ToolCalls {
			if call.Recovery != nil {
				attempts[call.Recovery.Identity.AttemptID] = true
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(p.ToolCallEvidence)) {
		if present[id] {
			continue
		}
		for _, message := range evidenceMessages(id, p.ToolCallEvidence[id]) {
			attempt := message.ToolCalls[0].Recovery.Identity.AttemptID
			if !attempts[attempt] {
				p.ModelMessages = append(p.ModelMessages, message)
				attempts[attempt] = true
			}
		}
	}
}
