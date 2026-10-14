package provider

import (
	"deepzora/internal/attachment"
	"slices"
)

// Two copies of a transcript are derived from the stored one: the bytes a
// provider receives, and the projection compaction writes back. They differ in
// exactly one field, and that difference is load-bearing.

// ModelMessages removes durable display-only records before a request is
// handed to any provider. Healthy sessions without such records keep their
// original backing slice, preserving the allocation and prompt-cache fast path.
func ModelMessages(msgs []Message) []Message { return projectMessages(msgs, false, false) }

// ProjectionMessages is ModelMessages for a stored projection, except that
// ToolExecution and Origin survive: a projection is also the next compaction's
// input, and those records classify tool failures and host-authored protocol
// messages. Stripping belongs at the provider boundary, which every request
// path already crosses.
func ProjectionMessages(msgs []Message) []Message { return projectMessages(msgs, true, true) }

func messagesNeedProjection(msgs []Message, keepExecution, keepOrigin bool) bool {
	for _, m := range msgs {
		if !keepExecution && len(m.ImageInputs) > 0 {
			return true
		}
		if m.InterruptedTurn != nil || slices.ContainsFunc(m.ToolCalls, func(c ToolCall) bool { return c.Recovery != nil }) || m.ReadPause != nil || m.ReadCompletion != nil || len(m.ToolDiagnostic) > 0 {
			return true
		}
		if slices.ContainsFunc(m.ServerSearch, func(s ServerSearchCall) bool { return s.SourcesStatus != "" }) || len(m.ProtocolRecovery) > 0 || (!keepExecution && slices.ContainsFunc(m.ToolCalls, func(c ToolCall) bool { return len(c.WriteIntents) > 0 })) || m.LocalOnly || (!keepOrigin && m.Origin != "") || m.RawContent != "" || m.DecisionReceipt != nil || len(m.DecisionReceipts) > 0 || m.VisionSummary != nil || m.MCPApp != nil || len(m.ReadResult) > 0 || (!keepExecution && m.PresentedFiles != nil) || ((m.ToolExecution != nil || m.ToolRunState != "") && !keepExecution) {
			return true
		}
	}
	return false
}

func projectMessages(msgs []Message, keepExecution, keepOrigin bool) []Message {
	if !messagesNeedProjection(msgs, keepExecution, keepOrigin) {
		return msgs
	}
	out := make([]Message, 0, len(msgs))
	for _, candidate := range msgs {
		if candidate.LocalOnly {
			continue
		}
		candidate.RawContent = ""
		candidate.ProtocolRecovery = nil
		// Read delivery envelopes are host evidence; they must never change
		// provider bytes.
		candidate.ReadResult = nil
		candidate.ReadPause = nil
		candidate.InterruptedTurn = nil
		candidate.ReadCompletion = nil
		candidate.ToolDiagnostic = nil
		if !keepExecution && slices.ContainsFunc(candidate.ServerSearch, func(s ServerSearchCall) bool { return s.SourcesStatus != "" }) {
			candidate.ServerSearch = append([]ServerSearchCall(nil), candidate.ServerSearch...)
			for i := range candidate.ServerSearch {
				candidate.ServerSearch[i].SourcesStatus = ""
			}
		}
		if !keepOrigin {
			candidate.Origin = ""
		}
		candidate.DecisionReceipt = nil
		candidate.DecisionReceipts = nil
		candidate.VisionSummary = nil
		// Apps presentation stays local; it must never change provider bytes.
		candidate.MCPApp = nil
		if !keepExecution {
			// Standalone Agent messages can contain current URL/files inputs.
			// Content references are resolved by the controller before this boundary.
			if len(candidate.Images) == 0 && len(candidate.ImageInputs) > 0 && !slices.ContainsFunc(candidate.ImageInputs, func(input attachment.ImageInput) bool { return input.Kind == attachment.KindAttachment }) {
				for _, input := range candidate.ImageInputs {
					switch input.Kind {
					case attachment.KindURL:
						candidate.Images = append(candidate.Images, input.URL)
					case attachment.KindFiles:
						candidate.Images = append(candidate.Images, input.FilesID)
					}
				}
				candidate.ImageInputs = nil
			}
			// Local shell metadata must never enter provider request bytes.
			candidate.ToolExecution = nil
			candidate.ToolRunState = ""
			candidate.PresentedFiles = nil
			if slices.ContainsFunc(candidate.ToolCalls, func(c ToolCall) bool { return len(c.WriteIntents) > 0 || c.Recovery != nil }) {
				candidate.ToolCalls = append([]ToolCall(nil), candidate.ToolCalls...)
				for i := range candidate.ToolCalls {
					candidate.ToolCalls[i].WriteIntents = nil
					candidate.ToolCalls[i].Recovery = nil
				}
			}
		}
		out = append(out, candidate)
	}
	return out
}
