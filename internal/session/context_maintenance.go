package session

import (
	"encoding/json"
	"errors"

	"deepzora/internal/agent"
)

const ContextMaintenanceKind = "context/maintenance"

func ContextMaintenanceEvent(state agent.SessionContextMaintenance) (Event, error) {
	payload, err := json.Marshal(state)
	return Event{Kind: ContextMaintenanceKind, Payload: payload}, err
}

func projectContextMaintenance(projection *Projection, ev Event) error {
	var state agent.SessionContextMaintenance
	if err := strictPayload(ev.Payload, &state); err != nil {
		return damagedPayload(ev, err)
	}
	if state.LineageKey == "" || state.Generation == 0 || state.Receipt == nil || state.Receipt.OperationID == "" {
		return damagedPayload(ev, errors.New("context maintenance requires lineage and receipt identity"))
	}
	if state.Receipt.ProjectionVersion != state.ProjectionVersion {
		return damagedPayload(ev, errors.New("context maintenance projection version mismatch"))
	}
	switch state.Receipt.Status {
	case "applied", "blocked", "failed":
	default:
		return damagedPayload(ev, errors.New("invalid context maintenance outcome"))
	}
	projection.ContextMaintenance = &state
	return nil
}
