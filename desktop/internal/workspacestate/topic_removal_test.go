package workspacestate

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestTopicRemovalJournalRetainsPreparedPhaseAfterFailedCommit(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err := s.TransitionTopicRemoval(t.Context(), "remove", func(_ State, item *TopicRemoval) error {
		*item = TopicRemoval{ID: "remove", TopicID: "topic", WorkspaceID: GlobalWorkspaceID, Phase: "prepared", Token: "confirmed", SessionToken: "confirmed", Disposition: "archive_sessions"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("archive unavailable")
	err := s.TransitionTopicRemoval(t.Context(), "remove", func(_ State, item *TopicRemoval) error { item.Phase = "committed"; return failed })
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
	state, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state.TopicRemovals["remove"].Phase != "prepared" {
		t.Fatal("failed commit was published")
	}
	before := state.Generation
	if err := s.TransitionTopicRemoval(t.Context(), "remove", func(_ State, item *TopicRemoval) error { item.Phase = "committed"; return nil }); err != nil {
		t.Fatal(err)
	}
	state, err = s.Load(t.Context())
	if err != nil || state.Generation <= before || state.TopicRemovals["remove"].Phase != "committed" {
		t.Fatalf("completion not published: %+v %v", state, err)
	}
}

func TestTopicRemovalUnknownFieldsRoundTrip(t *testing.T) {
	var item TopicRemoval
	if err := json.Unmarshal([]byte(`{"id":"remove","phase":"prepared","future":{"keep":[1,2]}}`), &item); err != nil {
		t.Fatal(err)
	}
	item.Phase = "committed"
	body, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["future"]) != `{"keep":[1,2]}` {
		t.Fatal("future writer evidence lost")
	}
	state := newState()
	body, _ = json.Marshal(state)
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["topicRemovals"]; exists {
		t.Fatal("empty collection changed old-data shape")
	}
}
