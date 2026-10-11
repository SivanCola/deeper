package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"deepzora/internal/provider"
)

type messageRetractPayload struct {
	MessageIDs []string `json:"messageIds"`
	Reason     string   `json:"reason,omitempty"`
}

func retractedMessageIDs(event Event, payload json.RawMessage) ([]string, error) {
	var body messageRetractPayload
	if err := strictPayload(payload, &body); err != nil {
		return nil, damagedPayload(event, err)
	}
	if len(body.MessageIDs) == 0 {
		return nil, damagedPayload(event, fmt.Errorf("empty messageIds"))
	}
	seen := make(map[string]bool, len(body.MessageIDs))
	for _, id := range body.MessageIDs {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id || seen[id] {
			return nil, damagedPayload(event, fmt.Errorf("invalid or duplicate message id"))
		}
		seen[id] = true
	}
	return body.MessageIDs, nil
}

// Execution, recovery, history and search must decode the same durable event
// schema. Projection-specific DTOs used to reject valid rewrite/import metadata.
type historyReplacePayload struct {
	Messages []provider.Message `json:"messages"`
	Reason   string             `json:"reason,omitempty"`
	Sources  []uint64           `json:"sourceSequences,omitempty"`
}

// ErrDuplicateMessageID refuses a write that would give two messages one
// stable id; every reader of the log keys a message by that id.
var ErrDuplicateMessageID = errors.New("session: duplicate stable message id")

func replacementEventMessages(event Event, payload json.RawMessage) ([]provider.Message, error) {
	messages, err := decodeReplacementMessages(event, payload)
	if err != nil {
		return nil, err
	}
	if id, duplicate := duplicateMessageID(messages); duplicate {
		return nil, damagedPayload(event, fmt.Errorf("%w: %s", ErrDuplicateMessageID, id))
	}
	return messages, nil
}

func decodeReplacementMessages(event Event, payload json.RawMessage) ([]provider.Message, error) {
	var body historyReplacePayload
	if err := strictPayload(payload, &body); err != nil || body.Messages == nil {
		return nil, damagedPayload(event, err)
	}
	return body.Messages, nil
}

func duplicateMessageID(messages []provider.Message) (string, bool) {
	seen := make(map[string]bool, len(messages))
	for _, message := range messages {
		if message.ID == "" {
			continue
		}
		if seen[message.ID] {
			return message.ID, true
		}
		seen[message.ID] = true
	}
	return "", false
}

// checkReplacementIdentities refuses a new replacement that repeats an id. A
// payload that does not decode is the projection's to refuse.
func checkReplacementIdentities(event Event) error {
	if event.Kind != "history/replace" {
		return nil
	}
	messages, err := decodeReplacementMessages(event, event.Payload)
	if err != nil {
		return nil
	}
	if id, duplicated := duplicateMessageID(messages); duplicated {
		return fmt.Errorf("%w: %s message %q", ErrDuplicateMessageID, event.Kind, id)
	}
	return nil
}
