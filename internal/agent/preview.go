package agent

import (
	"regexp"
	"strings"

	"deepzora/internal/event"
	"deepzora/internal/provider"
)

// TransientUserBlockTags names every block the host prepends to a user turn as
// runtime context rather than something the user typed. Previews, titles, and
// the rewind picker strip them; a tag missing from this list leaks raw markup
// into the UI, which is how <autoresearch-runtime> surfaced in session titles.
//
// This is the single source of truth: the strip regex is built from it, and
// hasLeadingInjectedBlock walks it. Anything that starts prepending a new block
// to user turns belongs here.
var TransientUserBlockTags = []string{
	"response-language",
	"reasoning-language",
	"memory-update",
	"background-jobs",
	"active-goal",
	"autoresearch-runtime",
	"hook-context",
	"capability-route",
	"interrupted-turn-recovery",
	"execution-policy",
}

// reTrailingExecutionPolicy matches the host-appended execution-policy block at
// the end of a user turn (attributes allowed on the open tag).
var reTrailingExecutionPolicy = regexp.MustCompile(`(?s)\n*<execution-policy(?:\s+[^>]*)?>.*?</execution-policy>\s*$`)

var reTransientUserBlock = buildTransientUserBlockRE(TransientUserBlockTags)

// buildTransientUserBlockRE matches one leading transient block: an open tag
// (with optional attributes), its content, and its own closing tag. The
// alternation is generated so the open and close lists cannot drift apart —
// spelling them out twice by hand is what let tags go missing from one side.
func buildTransientUserBlockRE(tags []string) *regexp.Regexp {
	alt := strings.Join(tags, "|")
	return regexp.MustCompile(`(?s)^\s*<(?:` + alt + `)(?:\s+[^>]*)?>.*?</(?:` + alt + `)>\s*\n?`)
}

// stripTrailingDeliveryRuntime removes the exact delivery-runtime marker the
// agent appends to user turns in delivery mode (agent.go DeliveryRuntimeMarker).
// Unlike the prefix blocks it trails the user text, so preview/title derivation
// needs a suffix cut — leaving it produced session titles like
// "你是谁？ <delivery-run…". The cut is byte-exact rather than a regex: a lazy
// pattern anchored at $ would swallow user prose between a literal
// "<delivery-runtime>" mention in the text and the real marker at the end.
// (The agent never appends the marker when the input already mentions the tag,
// so user messages discussing it carry no host suffix at all.)
func stripTrailingDeliveryRuntime(s string) string {
	trimmed := strings.TrimRight(s, " \t\r\n")
	if cut, ok := strings.CutSuffix(trimmed, DeliveryRuntimeMarker); ok {
		return strings.TrimRight(cut, " \t\r\n")
	}
	return s
}

// StripTransientUserBlocks removes controller-injected transient XML blocks
// from persisted user messages before deriving display text, previews, or
// titles. The blocks are sent in user turns so they never affect the stable
// prompt prefix, but they should not become user-facing text later.
func StripTransientUserBlocks(content string) string {
	s := content
	for {
		next := reTransientUserBlock.ReplaceAllStringFunc(s, func(string) string {
			return ""
		})
		if next == s {
			break
		}
		s = next
	}
	s = stripTrailingDeliveryRuntime(s)
	s = reTrailingExecutionPolicy.ReplaceAllString(s, "")
	s = stripTrailingMemoryRecall(s)
	return strings.TrimLeft(s, " \t\r\n")
}

func stripTrailingMemoryRecall(s string) string {
	trimmed := strings.TrimRight(s, " \t\r\n")
	const open = "<memory-recall>"
	const close = "</memory-recall>"
	if !strings.HasSuffix(trimmed, close) {
		return s
	}
	if index := strings.LastIndex(trimmed, open); index >= 0 {
		return strings.TrimRight(trimmed[:index], " \t\r\n")
	}
	return s
}

// UserPreviewText returns the user-authored part of a persisted user message.
func UserPreviewText(content string) string {
	s := StripTransientUserBlocks(content)
	s = HandoffTask(s)
	s = StripTransientUserBlocks(s)
	return strings.TrimSpace(s)
}

// pasteDisplayLabelPattern matches the standalone label desktop prepends to a
// pasted-text turn. It is UI chrome rather than user intent, so title and
// preview derivation may remove it without touching inline label mentions.
var pasteDisplayLabelPattern = regexp.MustCompile(`^\[(?:已粘贴文本|已貼上文字|Pasted text) #[0-9]+ · [0-9]+ (?:行|lines)\][ \t]*(?:\r?\n)?`)

// StripPasteDisplayLabel removes one leading desktop pasted-text label while
// preserving the remainder byte-for-byte.
func StripPasteDisplayLabel(content string) string {
	return pasteDisplayLabelPattern.ReplaceAllString(content, "")
}

// UserMessageText returns the best user-authored view of a persisted user turn.
// New sessions carry the exact raw text explicitly; older sessions fall back to
// deterministic wrapper stripping.
func UserMessageText(msg provider.Message) string {
	if msg.RawContent != "" {
		return strings.TrimSpace(msg.RawContent)
	}
	return strings.TrimSpace(msg.Content)
}

// emitAdmittedUserMessage publishes the display identity of a persisted
// user-authored turn message. Host-injected messages stay silent.
func emitAdmittedUserMessage(sink event.Sink, user provider.Message) {
	if IsUserAuthoredTurnMessage(user) {
		sink.Emit(event.Event{Kind: event.UserMessage, MessageID: user.ID, Text: UserMessageText(user)})
	}
}

// Auto Guard writes these onto the failed tool result for the model. Older
// sessions may still have them persisted as mid-turn user steers; display
// paths must hide those so they never appear as the user's own words.
const (
	HostRecoveryGuidanceToolFailedPrefix = "A tool failed. Use read-only diagnosis as needed"
	HostRecoveryGuidanceTransientPrefix  = "The tool timed out or hit a transient execution limit."
	ReadinessContinuationPrefix          = "This turn ended with work still outstanding:"
	StandardTodoContinuationPrefix       = "The current task list still has an in-progress item."
)

// IsHostRecoveryGuidance reports model-facing Auto Guard policy text.
func IsHostRecoveryGuidance(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if after, ok := strings.CutPrefix(trimmed, "↪ "); ok {
		trimmed = strings.TrimSpace(after)
	}
	return strings.HasPrefix(trimmed, HostRecoveryGuidanceToolFailedPrefix) ||
		strings.HasPrefix(trimmed, HostRecoveryGuidanceTransientPrefix)
}

// VisibleSteerText is the user-authored mid-turn steer the transcript may
// show. Host Auto Guard policy is not user-authored and must stay hidden.
func VisibleSteerText(content string) (string, bool) {
	text, handled := ReplaySteerText(content)
	if !handled || text == "" {
		return "", false
	}
	return text, true
}

// ReplaySteerText reports a persisted steer for display replay. handled is
// true for any steer; text is empty when host Auto Guard policy must be omitted.
func ReplaySteerText(content string) (text string, handled bool) {
	text, isSteer := SteerText(content)
	if !isSteer {
		return "", false
	}
	if IsHostRecoveryGuidance(text) {
		return "", true
	}
	return text, true
}

// IsHostGeneratedUserMessage reports whether a user-role message came from the
// host. Explicit provenance is authoritative.
func IsHostGeneratedUserMessage(msg provider.Message) bool {
	if msg.Role != provider.RoleUser {
		return false
	}
	switch msg.Origin {
	case provider.MessageOriginHost:
		return true
	case provider.MessageOriginUser:
		return false
	default:
		return false
	}
}

// IsUserAuthoredTurnMessage reports whether a persisted message begins a real
// visible user turn. Mid-turn steers are user-authored but do not start turns.
func IsUserAuthoredTurnMessage(msg provider.Message) bool {
	if msg.Role != provider.RoleUser || IsHostGeneratedUserMessage(msg) {
		return false
	}
	content := UserMessageText(msg)
	if strings.TrimSpace(StripTransientUserBlocks(content)) == "" {
		return false
	}
	// RawContent is the user's exact steer text and deliberately omits the
	// provider wrapper. Turn classification must inspect stored Content, while
	// evidence/display continue to prefer RawContent.
	_, isSteer := SteerText(msg.Content)
	return !isSteer
}
