package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"deepzora/internal/provider"
)

// ContentDigest captures the content of the current in-memory transcript.
func (s *Session) ContentDigest() (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil session")
	}
	return ContentDigestForMessages(s.Snapshot())
}

// ContentDigestForMessages binds a rendered history snapshot to its content.
// Message identity and timing are carried separately from the content digest.
func ContentDigestForMessages(messages []provider.Message) (string, error) {
	h := sha256.New()
	for _, message := range messages {
		body, err := json.Marshal(messageForContentComparison(message))
		if err != nil {
			return "", err
		}
		_, _ = h.Write(body)
		_, _ = h.Write([]byte{'\n'})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func messageForContentComparison(message provider.Message) provider.Message {
	message.ID, message.CreatedAt = "", 0
	return message
}

func messagesEqualForContent(a, b []provider.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		left, err := json.Marshal(messageForContentComparison(a[i]))
		if err != nil {
			return false
		}
		right, err := json.Marshal(messageForContentComparison(b[i]))
		if err != nil || !bytes.Equal(left, right) {
			return false
		}
	}
	return true
}
