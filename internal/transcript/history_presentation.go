package transcript

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"deepzora/internal/event"
	"deepzora/internal/i18n"
	"deepzora/internal/provider"
)

var pastedLabel = regexp.MustCompile(`^\[(?:已粘贴文本|已貼上文字|Pasted text) #[0-9]+ · [0-9]+ (?:行|lines)\]$`)

// CollapseExpandedPaste keeps the display card while SubmitText retains the
// original expanded payload used to reconstruct an edited prompt.
func CollapseExpandedPaste(content string) string {
	const prefix = "--- Begin "
	for scan := 0; scan < len(content); {
		begin := strings.Index(content[scan:], prefix)
		if begin < 0 {
			break
		}
		begin += scan
		start := begin + len(prefix)
		end := strings.Index(content[start:], " ---")
		if end < 0 {
			break
		}
		end += start
		label := content[start:end]
		body := end + len(" ---")
		scan = body
		if !pastedLabel.MatchString(label) {
			continue
		}
		marker := "--- End " + label + " ---"
		last := strings.Index(content[body:], marker)
		if last < 0 {
			continue
		}
		copy := strings.LastIndex(content[:begin], label)
		if copy < 0 || strings.TrimSpace(content[copy+len(label):begin]) != "" {
			continue
		}
		content = content[:copy+len(label)] + content[body+last+len(marker):]
		scan = copy + len(label)
	}
	return strings.TrimSpace(content)
}

func interruptedNotice(recovery *provider.InterruptedTurnRecovery) Message {
	if recovery != nil && recovery.TerminalStatus == "failed" {
		diagnostic := recovery.FailureDiagnostic
		text := "The provider request failed. Check the connection settings and try again."
		if diagnostic != nil {
			if status := i18n.M.ProviderStatusMessage(diagnostic.Status); status != "" {
				text = status
			} else if diagnostic.Status > 0 {
				text = fmt.Sprintf("Provider request failed (HTTP %d).", diagnostic.Status)
			}
			if label := provider.ProviderDisplayLabel(diagnostic.ProviderID, diagnostic.ProviderDisplayName, diagnostic.Protocol); label != "" {
				text = label + ": " + text
			}
		}
		return Message{Role: "notice", Level: "warn", Code: event.NoticeCodeProviderRequestFailed, Content: text, Detail: provider.FailureDiagnosticDetail(diagnostic), Diagnostic: diagnostic}
	}
	return Message{Role: "notice", Level: "info", Code: event.NoticeCodeCancelledTurn,
		Content: "This turn was interrupted. Partial output is kept for reference; only completed tool pairs and a bounded recovery summary enter the next model turn. Inspect the workspace before continuing or reverting changes."}
}

func toolFailed(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "error:") || strings.HasPrefix(content, "blocked:") || strings.HasPrefix(content, "Error:") || strings.HasPrefix(content, "[error")
}

// currentTodoArguments uses the current tool's normalized result for display.
// It never changes another call's list or infers progress from retired tools.
func currentTodoArguments(messages []provider.Message) map[string]string {
	calls := make(map[string]bool)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Name == "todo_write" && call.ID != "" {
				calls[call.ID] = true
			}
		}
	}
	out := make(map[string]string)
	for _, message := range messages {
		if message.Role != provider.RoleTool || !calls[message.ToolCallID] || toolFailed(message.Content) {
			continue
		}
		var result struct {
			Todos []struct {
				Content string `json:"content"`
				Status  string `json:"status"`
			} `json:"todos"`
		}
		if json.Unmarshal([]byte(message.Content), &result) != nil || result.Todos == nil {
			continue
		}
		if encoded, err := json.Marshal(result); err == nil {
			out[message.ToolCallID] = string(encoded)
		}
	}
	return out
}
