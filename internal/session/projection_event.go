package session

func applyProjectionEvent(projection *Projection, commit Commit, ev Event, presetLocked bool) error {
	switch ev.Kind {
	case ContextMaintenanceKind:
		return projectContextMaintenance(projection, ev)
	case StreamCheckpointKind:
		projectStreamCheckpoint(projection, ev)
	case "submission/accepted":
		return projectSubmission(projection, commit, ev)
	case "message/complete":
		return projectMessageComplete(projection, commit, ev)
	case "message/upsert":
		return projectMessageUpsert(projection, commit, ev)
	case "message/retract":
		return projectMessageRetract(projection, commit, ev)
	case "assistant/attempt":
		return projectAssistantAttempt(projection, commit, ev)
	case "history/replace":
		return projectHistoryReplace(projection, commit, ev)
	case "model/context-replace":
		return projectModelContextReplace(projection, commit, ev)
	case "session/title":
		return projectSessionTitle(projection, commit, ev)
	case "session/config", "session/permission-preset":
		return projectSessionSettings(projection, commit, ev)
	case AgentPresetKind:
		return projectAgentPreset(projection, ev, presetLocked)
	case "compaction":
		return projectCompaction(projection, commit, ev)
	case "turn/start":
		return projectTurnStart(projection, commit, ev)
	case "step/start", "step/end":
		return projectStepStart(projection, commit, ev)
	case "tool/call":
		return projectToolCall(projection, commit, ev)
	case "tool/start":
		return projectToolStart(projection, commit, ev)
	case "tool/result":
		return projectToolResult(projection, commit, ev)
	case "todo/write":
		return projectTodoWrite(projection, commit, ev)
	case "interaction/created":
		return projectInteractionCreated(projection, commit, ev)
	case "interaction/resolved":
		return projectInteractionResolved(projection, commit, ev)
	case "runtime/recovery":
		return projectRuntimeRecovery(projection, commit, ev)
	case "plan/state":
		return projectPlanState(projection, commit, ev)
	case "goal/state":
		return projectGoalState(projection, commit, ev)
	case "diagnostic":
		return projectDiagnostic(projection, commit, ev)
	case "turn/end":
		return projectTurnEnd(projection, commit, ev)
	}
	return nil
}
