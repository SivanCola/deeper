package session

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestReplacementEventSchemaRemainsStrict(t *testing.T) {
	for _, kind := range []string{"history/replace"} {
		for _, payload := range []string{`{"messages":[],"typo":true}`, `{"messages":null}`, `{}`} {
			event := Event{Kind: kind, Payload: json.RawMessage(payload)}
			if _, err := replacementEventMessages(event, event.Payload); !errors.Is(err, ErrDamagedStore) {
				t.Fatalf("%s accepted invalid payload %s: %v", kind, payload, err)
			}
		}
		messages, err := replacementEventMessages(Event{Kind: kind}, json.RawMessage(`{"messages":[]}`))
		if err != nil || messages == nil || len(messages) != 0 {
			t.Fatalf("%s rejected valid empty history: %v", kind, err)
		}
	}
}
