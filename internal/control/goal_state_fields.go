package control

import (
	"encoding/json"
	"fmt"
)

var goalStateKnownFields = map[string]struct{}{
	"schemaVersion": {}, "goal": {}, "status": {},
	"scopeID": {}, "deliveryCheckpoint": {},
	"block":     {},
	"turnsUsed": {}, "tokensUsed": {}, "requestsUsed": {},
	"workDurationMs": {},
	"tokensLimit":    {}, "noProgressTurns": {},
	"lastContinuationReason": {}, "lastEvaluatorReason": {}, "stopCause": {},
	"budgetExtensions": {}, "progressEvidence": {},
}

func goalStateUnknownFields(raw []byte) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	for key := range goalStateKnownFields {
		delete(fields, key)
	}
	return cloneGoalStateExtra(fields)
}

func marshalGoalState(state goalState, extra map[string]json.RawMessage) ([]byte, error) {
	known, err := json.Marshal(state)
	if err != nil || len(extra) == 0 {
		return known, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(known, &merged); err != nil {
		return nil, err
	}
	for key, value := range extra {
		if _, current := goalStateKnownFields[key]; current {
			continue
		}
		merged[key] = append(json.RawMessage(nil), value...)
	}
	return json.Marshal(merged)
}

func cloneGoalStateExtra(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for key, value := range in {
		out[key] = append(json.RawMessage(nil), value...)
	}
	return out
}

func (g *goalMachine) grantSpendSliceLocked(fresh bool) {
	switch {
	case g.tokenBudget <= 0:
		g.tokensLimit = 0
	case fresh || g.tokensLimit <= g.tokensUsed:
		g.tokensLimit = g.tokensUsed + g.tokenBudget
	}
}

func decodeGoalState(raw []byte) (goalState, error) {
	var state goalState
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if state.SchemaVersion != 1 {
		return state, fmt.Errorf("unsupported goal schema: %d", state.SchemaVersion)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return state, err
	}
	for _, key := range []string{"strict", "todos", "todo", "activeForm", "step_id", "auto_continue", "autoContinue", "budgetClass", "researchMode", "autoResearchTaskID"} {
		if _, ok := fields[key]; ok {
			return state, fmt.Errorf("retired goal field: %s", key)
		}
	}
	return state, nil
}
