package workspacestate

import (
	"context"
	"encoding/json"
)

// TopicRemoval retains the confirmed current session set and archive receipt.
type TopicRemoval struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspaceId"`
	TopicID      string `json:"topicId"`
	Token        string `json:"token"`
	SessionToken string `json:"sessionToken,omitempty"`
	Disposition  string `json:"disposition"`
	Phase        string `json:"phase"`
	extra        map[string]json.RawMessage
}

// TransitionTopicRemoval publishes each current archive phase under the
// registry writer lock. A failed transaction never publishes a partial phase.
func (s *Store) TransitionTopicRemoval(ctx context.Context, id string, transaction func(State, *TopicRemoval) error) error {
	return s.mutate(ctx, func(state *State) error {
		item := state.TopicRemovals[id]
		if err := transaction(*state, &item); err != nil {
			return err
		}
		state.TopicRemovals[id] = item
		return nil
	})
}

func (r *TopicRemoval) UnmarshalJSON(body []byte) error {
	type plain TopicRemoval
	var decoded plain
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	for _, key := range []string{"id", "workspaceId", "topicId", "token", "sessionToken", "disposition", "phase"} {
		delete(fields, key)
	}
	*r = TopicRemoval(decoded)
	r.extra = fields
	return nil
}

func (r TopicRemoval) MarshalJSON() ([]byte, error) {
	type plain TopicRemoval
	body, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	return mergeUnknown(body, r.extra)
}
