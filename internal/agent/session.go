// Package agent wires a Provider, a tool Registry, and a Session into the
// harness loop that drives a coding task to completion.
package agent

import (
	"strings"
	"sync"

	"deepzora/internal/provider"
)

// Session holds the conversation history for one task. The run loop (one turn at
// a time) is the only writer, but a frontend can read History/Save from another
// goroutine while a turn appends, so mu guards Messages. Direct Messages reads on
// the run-loop goroutine stay lock-free (serial with its own writes); cross-
// goroutine access goes through Snapshot.
type Session struct {
	cacheSessionID string // ephemeral transport identity; never model-visible or persisted
	mu             sync.RWMutex
	Messages       []provider.Message
	version        uint64
	rewriteVersion int // bumped each time the log is rewritten (compact/fold)
	// pendingContentReasons records provider-visible rewrites for cache-prefix
	// accounting. Local metadata changes do not contribute a content reason.
	pendingContentReasons []string
}

// NewSession initializes a session with an optional system prompt.
func NewSession(system string) *Session {
	s := &Session{}
	if system != "" {
		s.Messages = append(s.Messages, provider.Message{Role: provider.RoleSystem, Content: system, ID: NewMessageID()})
	}
	return s
}

// Add appends a message.
func (s *Session) Add(m provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.ID == "" {
		m.ID = NewMessageID()
	}
	s.expireProtocolRecoveryLocked([]provider.Message{m})
	s.Messages = append(s.Messages, m)
	s.version++
}

// AddBatch appends one logical transcript batch under a single lock. Turn
// admission uses it for an optional host context revision plus the real user
// message so autosave can never observe only half of the admitted boundary.
func (s *Session) AddBatch(messages ...provider.Message) {
	if s == nil || len(messages) == 0 {
		return
	}
	s.mu.Lock()
	mintMessageIDs(messages)
	s.expireProtocolRecoveryLocked(messages)
	s.Messages = append(s.Messages, messages...)
	s.version++
	s.mu.Unlock()
}

// SetLeadingSystemPrompt updates or sets the leading system prompt message.
func (s *Session) SetLeadingSystemPrompt(prompt string) {
	s.SetLeadingSystemPromptWithReason(prompt, "system_prompt_refresh")
}

// SetLeadingSystemPromptWithReason refreshes the authoritative system prompt
// and records the provider-visible rewrite boundary exactly once. It is used
// for low-frequency host prompt migrations, never ordinary pinned-context
// updates (which append user-role revisions instead).
func (s *Session) SetLeadingSystemPromptWithReason(prompt, reason string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Messages) > 0 && s.Messages[0].Role == provider.RoleSystem {
		if s.Messages[0].Content == prompt {
			return false
		}
		s.Messages[0].Content = prompt
	} else if prompt != "" {
		s.Messages = append([]provider.Message{{Role: provider.RoleSystem, Content: prompt, ID: NewMessageID()}}, s.Messages...)
	} else {
		return false
	}
	s.rewriteVersion++
	if reason = strings.TrimSpace(reason); reason != "" {
		s.pendingContentReasons = append(s.pendingContentReasons, reason)
	}
	s.version++
	return true
}

// AddDecisionReceipt persists local decision metadata without inserting a
// standalone message into the current tool turn. Tool results must remain
// directly adjacent to the assistant message that requested them; otherwise
// provider message validation can reject the real result. Attaching to the newest assistant message keeps the
// provider-visible transcript byte-for-byte equivalent after ModelMessages.
//
// A local-only record covers host decisions made before any assistant message
// exists. ModelMessages excludes it from provider requests.
func (s *Session) AddDecisionReceipt(receipt *provider.DecisionReceipt) {
	if s == nil || receipt == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	//nolint:modernize // slices.Backward yields element copies; this body writes through the index.
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if IsUserAuthoredTurnMessage(s.Messages[i]) {
			break
		}
		if s.Messages[i].Role != provider.RoleAssistant || s.Messages[i].LocalOnly {
			continue
		}
		receipts := append([]*provider.DecisionReceipt(nil), s.Messages[i].DecisionReceipts...)
		s.Messages[i].DecisionReceipts = append(receipts, receipt)
		// A mid-turn snapshot may already contain this assistant message. Force
		// the next save to replace it instead of treating the later tool result
		// as the only append-only change.
		s.rewriteVersion++
		s.version++
		return
	}
	s.Messages = append(s.Messages, provider.Message{
		Role:            provider.RoleTool,
		ToolCallID:      "",
		Name:            "",
		LocalOnly:       true,
		DecisionReceipt: receipt,
	})
	s.version++
}

// UpdateToolCallPreview replaces the preview fields of the newest matching
// assistant tool call. A dependent writer can only be previewed after an
// earlier writer in the same model batch succeeds; updating under the session
// lock keeps live History/Snapshot readers race-free and ensures the refreshed
// preview is what a resumed session archives.
func (s *Session) UpdateToolCallPreview(call provider.ToolCall) bool {
	if call.ID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	//nolint:modernize // slices.Backward yields element copies; this body writes through the index.
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role != provider.RoleAssistant {
			continue
		}
		calls := s.Messages[i].ToolCalls
		for j := range calls {
			if calls[j].ID != call.ID {
				continue
			}
			cloned := append([]provider.ToolCall(nil), calls...)
			cloned[j].Diff = call.Diff
			cloned[j].Added = call.Added
			cloned[j].Removed = call.Removed
			s.Messages[i].ToolCalls = cloned
			// A snapshot may have persisted the original assistant message while
			// its tools were still running. Mark this as a rewrite so a later
			// autosave replaces that message instead of misclassifying the tool
			// results as an append-only suffix.
			s.rewriteVersion++
			s.version++
			return true
		}
	}
	return false
}

// UpdateToolCallResolution persists the host-resolved target metadata for the
// newest matching stable proxy call. The model-visible Name/Arguments remain
// unchanged; this metadata exists only so live and reloaded frontends classify
// MCP readers and writers accurately.
func (s *Session) UpdateToolCallResolution(call provider.ToolCall) bool {
	if call.ID == "" || call.ResolvedReadOnly == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	//nolint:modernize // slices.Backward yields element copies; this body writes through the index.
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role != provider.RoleAssistant {
			continue
		}
		calls := s.Messages[i].ToolCalls
		for j := range calls {
			if calls[j].ID != call.ID {
				continue
			}
			cloned := append([]provider.ToolCall(nil), calls...)
			readOnly := *call.ResolvedReadOnly
			cloned[j].ResolvedName = call.ResolvedName
			cloned[j].CapabilityID = call.CapabilityID
			cloned[j].ResolvedReadOnly = &readOnly
			s.Messages[i].ToolCalls = cloned
			// A mid-turn snapshot may already contain the unresolved proxy call.
			// Force the next save to rewrite that assistant message with its
			// resolved local metadata.
			s.rewriteVersion++
			s.version++
			return true
		}
	}
	return false
}

// Replace swaps the whole message log without classifying the change as a
// persisted-history rewrite. Call Rewrite when a live session changes messages
// that a mid-turn snapshot may already have written.
func (s *Session) Replace(msgs []provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs = retainUnresolvedToolRecords(s.Messages, msgs)
	mintMessageIDs(msgs)
	s.Messages = msgs
	s.version++
}

// Rewrite atomically replaces the message log and marks it as a rewrite. The
// atomic classification matters when a periodic snapshot races compaction,
// pruning, or local metadata edits: a later autosave must use owned-rewrite
// conflict checks instead of mistaking the modified prefix for another writer.
//
// reason names the provider-visible change (e.g. "rewind_truncate",
// "guardian_merge") and is queued for the next DrainContentRewriteReasons
// call, which feeds cache-diagnostics attribution. Callers whose msgs only
// change local-only display metadata (never serialized to the provider) must
// use ReplaceLocalMetadata instead, so they don't misreport a cache-prefix
// change that never happened.
func (s *Session) Rewrite(msgs []provider.Message, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs = retainUnresolvedToolRecords(s.Messages, msgs)
	mintMessageIDs(msgs)
	s.Messages = msgs
	s.rewriteVersion++
	s.version++
	if reason != "" {
		s.pendingContentReasons = append(s.pendingContentReasons, reason)
	}
}

// ReplaceLocalMetadata atomically replaces the message log exactly like
// Rewrite (including the rewriteVersion bump that forces the next save to use
// owned-rewrite conflict checks), for callers that only changed local-only
// display metadata (e.g. marking a resubmitted message Edited) rather than any
// provider-visible byte. Unlike Rewrite, it never queues a cache-prefix-change
// reason, since ModelMessages strips or never serializes what changed.
func (s *Session) ReplaceLocalMetadata(msgs []provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs = retainUnresolvedToolRecords(s.Messages, msgs)
	mintMessageIDs(msgs)
	s.Messages = msgs
	s.rewriteVersion++
	s.version++
}

// DrainContentRewriteReasons returns and clears the reasons queued by Rewrite
// since the last drain. Called once per provider request (run_loop.go) so
// CompareShape can attribute a cache-prefix change to the operation that
// actually caused it.
func (s *Session) DrainContentRewriteReasons() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	reasons := s.pendingContentReasons
	s.pendingContentReasons = nil
	return reasons
}

// NoteContentRewrite queues a provider-visible prefix-change reason without
// mutating Messages. Projection installs and resume-time system migrations use
// this so cache diagnostics attribute the next request's miss while the
// canonical transcript and its persistence baseline stay intact.
func (s *Session) NoteContentRewrite(reason string) {
	if s == nil || reason == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingContentReasons = append(s.pendingContentReasons, reason)
}

// Snapshot returns a copy of the messages, safe to read from another goroutine
// while a turn appends. Frontends (History, Save) use it instead of touching the
// live slice.
func (s *Session) Snapshot() []provider.Message {
	msgs, _, _ := s.snapshotWithVersion()
	return msgs
}

// DisplayBaseline captures messages and their rewrite revision together.
// The controller calls this at an idle/admission boundary, before any new
// streaming event can commit to its display projection.
func (s *Session) DisplayBaseline() (messages []provider.Message, rewriteEpoch uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]provider.Message(nil), s.Messages...), uint64(s.rewriteVersion)
}

// Len returns the number of messages, safe to call from any goroutine.
func (s *Session) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Messages)
}

// MessageRange returns a copy of the messages in [start, end), clamped to the
// current log bounds, safe to read from another goroutine while a turn
// appends. Paging frontends use it to fetch a display window without paying
// for a Snapshot of the whole history.
func (s *Session) MessageRange(start, end int) []provider.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if start < 0 {
		start = 0
	}
	if end > len(s.Messages) {
		end = len(s.Messages)
	}
	if start >= end {
		return []provider.Message{}
	}
	return append([]provider.Message(nil), s.Messages[start:end]...)
}

// CloneWithMessages copies the live conversation bookkeeping and replaces its
// message snapshot without sharing mutable slices.
func (s *Session) CloneWithMessages(msgs []provider.Message) *Session {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	version := s.version
	if !messagesEqualForContent(s.Messages, msgs) {
		version++
	}
	return &Session{
		Messages:              append([]provider.Message(nil), msgs...),
		version:               version,
		rewriteVersion:        s.rewriteVersion,
		pendingContentReasons: append([]string(nil), s.pendingContentReasons...),
	}
}

// snapshotWithVersion returns the messages together with the version and
// rewriteVersion they were captured under, in one lock window: save paths
// persist exactly this rewriteVersion as the new baseline, so a rewrite that
// lands after the capture cannot be misrecorded as saved.
func (s *Session) snapshotWithVersion() ([]provider.Message, uint64, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]provider.Message(nil), s.Messages...), s.version, s.rewriteVersion
}

// snapshotMessagesVersion returns a copy of the messages with the transcript
// version, for projection validity checks that do not need rewriteVersion.
func (s *Session) snapshotMessagesVersion() ([]provider.Message, uint64) {
	msgs, version, _ := s.snapshotWithVersion()
	return msgs, version
}

// TranscriptVersion returns the current append/rewrite counter used by
// context-projection validity checks.
func (s *Session) TranscriptVersion() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// RewriteVersion returns the current rewrite version.
func (s *Session) RewriteVersion() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rewriteVersion
}

// IncrementRewrite bumps the rewrite version by 1.
func (s *Session) IncrementRewrite() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rewriteVersion++
	s.version++
}

// HasContent returns true when the session carries at least one user,
// assistant, or tool message — i.e. more than just a system prompt. An
// "empty" conversation that has never been used should not be persisted.
func (s *Session) HasContent() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.Messages {
		if m.Role != provider.RoleSystem {
			return true
		}
	}
	return false
}

// HasSystemMessage reports whether the session starts with a system message,
// which carries the agent's stable identity and behavioural contract. Sessions
// without one are not safe to persist: when reloaded the model has no identity
// context and falls back to its training-data defaults.
func (s *Session) HasSystemMessage() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Messages) > 0 && s.Messages[0].Role == provider.RoleSystem
}
