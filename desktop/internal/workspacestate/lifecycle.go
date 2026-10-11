package workspacestate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

const (
	Active   = "active"
	Archived = "archived"
	Deleted  = "deleted"
)

type SessionState struct {
	Lifecycle  string `json:"lifecycle"`
	Generation uint64 `json:"generation"`
	ArchivedAt int64  `json:"archivedAt,omitempty"`
	extra      map[string]json.RawMessage
}
type Presentation struct {
	TopicID   string `json:"topicId,omitempty"`
	Title     string `json:"title,omitempty"`
	Pinned    bool   `json:"pinned,omitempty"`
	SortOrder int    `json:"sortOrder"`
	extra     map[string]json.RawMessage
}
type Operation struct {
	RequestFingerprint string          `json:"requestFingerprint,omitempty"`
	Request            json.RawMessage `json:"request,omitempty"`
	Result             json.RawMessage `json:"result,omitempty"`
	ID                 string          `json:"id"`
	Kind               string          `json:"kind"`
	Phase              string          `json:"phase"`
	SessionIDs         []string        `json:"sessionIds"`
	WorkspaceID        string          `json:"workspaceId,omitempty"`
	Lifecycle          string          `json:"lifecycle"`
	ExpectedGeneration uint64          `json:"expectedGeneration"`
	ResultGeneration   uint64          `json:"resultGeneration,omitempty"`
	Presentation       *Presentation   `json:"presentation,omitempty"`
	extra              map[string]json.RawMessage
}

type PurgeState uint8

const (
	PurgeAbsent PurgeState = iota
	PurgeTombstoned
	PurgeContentRemoved
	PurgeCommitted
	PurgeInvalid
)

func validLifecycle(value string) bool {
	return value == Active || value == Archived || value == Deleted
}
func setLifecycle(state *State, id, lifecycle string) {
	current := state.SessionStates[id]
	if lifecycle == Archived && current.Lifecycle != Archived {
		current.ArchivedAt = time.Now().UnixMilli()
	}
	current.Lifecycle, current.Generation = lifecycle, state.Generation+1
	state.SessionStates[id] = current
}
func validateLifecycleState(state State) error {
	for id, item := range state.SessionStates {
		if strings.TrimSpace(id) == "" || !validLifecycle(item.Lifecycle) {
			return fmt.Errorf("%w: session lifecycle", ErrUnsupportedVersion)
		}
	}

	for key, item := range state.PendingOperations {
		if key == "" || item.ID != key || !validLifecycle(item.Lifecycle) {
			return errors.New("invalid session operation")
		}
		switch item.Kind {
		case "archive", "purge", "restore", "command":
		default:
			return fmt.Errorf("%w: operation kind", ErrUnsupportedVersion)
		}
		switch item.Phase {
		case "prepared", "content_ready", "committed":
			if item.Kind == "purge" && item.Phase != "committed" {
				return fmt.Errorf("%w: purge phase", ErrUnsupportedVersion)
			}
		case "tombstoned", "content_removed":
			if item.Kind != "purge" {
				return ErrUnsupportedVersion
			}
		default:
			return fmt.Errorf("%w: operation phase", ErrUnsupportedVersion)
		}
	}

	return nil
}

func (s *Store) SetLifecycle(ctx context.Context, ids []string, lifecycle string) error {
	return s.mutate(ctx, func(state *State) error {
		if !validLifecycle(lifecycle) {
			return ErrUnsupportedVersion
		}
		for _, id := range ids {
			if state.SessionStates[id].Lifecycle == Deleted {
				return ErrMutationConflict
			}
			if _, ok := sessionOwner(*state, id); !ok {
				return ErrSessionNotFound
			}
			if err := validateLifecycleHasNoPurge(*state, id); err != nil {
				return err
			}
		}
		for _, id := range ids {
			setLifecycle(state, id, lifecycle)
			if lifecycle == Active {
				owner, _ := sessionOwner(*state, id)
				workspace := state.Workspaces[owner]
				workspace.Visible = true
				state.Workspaces[owner] = workspace
			}
		}
		return nil
	})
}

func (s *Store) BeginOperation(ctx context.Context, op Operation) error {
	return s.mutate(ctx, func(state *State) error {
		for _, id := range op.SessionIDs {
			if state.SessionStates[id].Lifecycle == Deleted {
				return ErrMutationConflict
			}
		}
		if op.ID == "" || !validLifecycle(op.Lifecycle) {
			return ErrMutationConflict
		}
		if old, ok := state.PendingOperations[op.ID]; ok {
			if old.Kind != op.Kind || old.Lifecycle != op.Lifecycle || old.WorkspaceID != op.WorkspaceID || (len(op.SessionIDs) != 0 && !slices.Equal(old.SessionIDs, op.SessionIDs)) {
				return ErrMutationConflict
			}
			return nil
		}
		if op.ExpectedGeneration != 0 && op.ExpectedGeneration != state.Generation {
			return ErrMutationConflict
		}

		// Bind child admission to the original command's observed state,
		// not to a newer snapshot taken after content/ownership validation.
		if split := strings.LastIndex(op.ID, "-"); split > 0 {
			parent := state.PendingOperations[op.ID[:split]]
			for _, id := range op.SessionIDs {
				if parent.Kind == "command" && state.SessionStates[id].Generation > parent.ExpectedGeneration {
					return ErrMutationConflict
				}
			}
		}
		op.ExpectedGeneration = state.Generation
		op.Phase = "prepared"
		if op.SessionIDs == nil {
			op.SessionIDs = []string{}
		}
		state.PendingOperations[op.ID] = op
		return nil
	})
}

func (s *Store) ReserveOperationTargets(ctx context.Context, id string, ids []string) error {
	return s.mutate(ctx, func(state *State) error {
		op, ok := state.PendingOperations[id]
		if !ok || len(ids) == 0 {
			return ErrMutationConflict
		}
		if len(op.SessionIDs) != 0 && !slices.Equal(op.SessionIDs, ids) {
			return ErrMutationConflict
		}

		op.SessionIDs = append([]string{}, ids...)
		state.PendingOperations[id] = op
		return nil
	})
}

func (s *Store) PrepareOperationContent(ctx context.Context, id string, ids []string, presentation *Presentation) error {
	return s.mutate(ctx, func(state *State) error {
		op, ok := state.PendingOperations[id]
		if !ok {
			return ErrMutationConflict
		}
		if op.Phase != "prepared" {
			if !slices.Equal(op.SessionIDs, ids) || !reflect.DeepEqual(op.Presentation, presentation) {
				return ErrMutationConflict
			}
			return nil
		}
		if len(op.SessionIDs) != 0 && !slices.Equal(op.SessionIDs, ids) {
			return ErrMutationConflict
		}
		op.Phase, op.SessionIDs, op.Presentation = "content_ready", append([]string{}, ids...), presentation
		state.PendingOperations[id] = op
		return nil
	})
}

// CommitOperation publishes membership, lifecycle and provenance in one durable
// registry replacement. File publication must have been validated beforehand.
func (s *Store) CommitOperation(ctx context.Context, id string) error {
	return s.mutate(ctx, func(state *State) error {

		if err := validateTopicRemovalArchive(*state, state.PendingOperations[id]); err != nil {
			return err
		}
		return commitOperation(state, id, map[string]bool{})
	})
}

func commitOperation(state *State, id string, visiting map[string]bool) error {
	if visiting[id] {
		return ErrMutationConflict
	}
	visiting[id] = true
	defer delete(visiting, id)
	op, ok := state.PendingOperations[id]
	if !ok {
		return ErrMutationConflict
	}
	if op.Phase == "committed" {
		return nil
	}
	if op.Phase != "content_ready" || len(op.SessionIDs) == 0 {
		return ErrMutationConflict
	}

	for _, sessionID := range op.SessionIDs {
		if err := validateLifecycleHasNoPurge(*state, sessionID); err != nil {
			return err
		}
		if state.SessionStates[sessionID].Lifecycle == Deleted {
			return ErrMutationConflict
		}
		if current, exists := state.SessionStates[sessionID]; exists && current.Generation > op.ExpectedGeneration && current.Generation != state.Generation+1 {
			return ErrMutationConflict
		}
		owner, attached := sessionOwner(*state, sessionID)
		if attached && op.WorkspaceID != "" && owner != op.WorkspaceID {
			return ErrMutationConflict
		}
		if !attached {
			workspace, exists := state.Workspaces[op.WorkspaceID]
			if !exists {
				return ErrWorkspaceNotFound
			}
			workspace.SessionIDs = insertBefore(workspace.SessionIDs, sessionID, "")
			attachOrganizationSession(&workspace, sessionID, "")
			mirrorOrganizationOrder(&workspace)

			workspace.UpdatedAt = time.Now().UTC()
			state.Workspaces[workspace.ID] = workspace
			owner = workspace.ID
		}
		setLifecycle(state, sessionID, op.Lifecycle)
		if op.Lifecycle == Active {
			workspace := state.Workspaces[owner]
			workspace.Visible = true
			state.Workspaces[owner] = workspace
		}
		if op.Presentation != nil {
			state.Presentation[sessionID] = *op.Presentation
		}
	}

	op.Phase, op.ResultGeneration = "committed", state.Generation+1
	state.PendingOperations[id] = op
	return nil
}

func ClassifyPurge(state State, id string) PurgeState {
	key := "purge-" + id
	op, exists := state.PendingOperations[key]
	if !exists {
		return PurgeAbsent
	}
	status, known := state.SessionStates[id]
	if !known || op.ID != key || op.Kind != "purge" || op.Lifecycle != Deleted || len(op.SessionIDs) != 1 || op.SessionIDs[0] != id {
		return PurgeInvalid
	}
	switch op.Phase {
	case "tombstoned":
		if status.Lifecycle == Deleted {
			return PurgeTombstoned
		}
	case "content_removed":
		if status.Lifecycle == Deleted {
			return PurgeContentRemoved
		}
	case "committed":
		if status.Lifecycle == Deleted {
			return PurgeCommitted
		}
	}
	return PurgeInvalid
}

func validateLifecycleHasNoPurge(state State, id string) error {
	switch ClassifyPurge(state, id) {
	case PurgeAbsent:
		return nil
	default:
		return ErrMutationConflict
	}
}

func samePurgeIdentity(left, right Operation) bool {
	return left.ID == right.ID && left.Kind == "purge" && right.Kind == "purge" && left.Lifecycle == right.Lifecycle &&
		left.ExpectedGeneration == right.ExpectedGeneration && slices.Equal(left.SessionIDs, right.SessionIDs) && bytes.Equal(left.Request, right.Request)
}

// BeginPurge atomically validates the archived generation, publishes the
// deletion tombstone and records the resumable purge operation.
func (s *Store) BeginPurge(ctx context.Context, id string, expected uint64) error {
	return s.beginOrResumePurge(ctx, id, expected, nil)
}

// ResumePurge continues only the observed operation. It cannot recreate a
// deletion intent after a restore superseded that operation.
func (s *Store) ResumePurge(ctx context.Context, id string, observed Operation) error {
	return s.beginOrResumePurge(ctx, id, observed.ExpectedGeneration, &observed)
}

// ResumePurgeForRequest keeps both the request snapshot and the observed
// transaction identity. Neither may be refreshed while waiting for locks.
func (s *Store) ResumePurgeForRequest(ctx context.Context, id string, expected uint64, observed Operation) error {
	return s.beginOrResumePurge(ctx, id, expected, &observed)
}

func (s *Store) beginOrResumePurge(ctx context.Context, id string, expected uint64, observed *Operation) error {
	err := s.mutate(ctx, func(state *State) error {
		key := "purge-" + id
		current, exists := state.PendingOperations[key]
		if observed != nil && (!exists || !samePurgeIdentity(current, *observed)) {
			return fmt.Errorf("%w: observed purge was removed or replaced", ErrMutationConflict)
		}
		switch ClassifyPurge(*state, id) {
		case PurgeTombstoned, PurgeContentRemoved, PurgeCommitted:
			return nil
		case PurgeInvalid:
			return fmt.Errorf("%w: inconsistent purge state", ErrMutationConflict)
		case PurgeAbsent:
			if observed != nil {
				return ErrMutationConflict
			}
			status, known := state.SessionStates[id]
			if !known || status.Lifecycle != Archived || status.Generation > expected {
				return ErrMutationConflict
			}
			op := Operation{ID: key, Kind: "purge", Phase: "tombstoned", Lifecycle: Deleted, SessionIDs: []string{id}, ExpectedGeneration: status.Generation}

			state.PendingOperations[key] = op
			setLifecycle(state, id, Deleted)
			return nil
		default:
			return ErrMutationConflict
		}
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) AdvancePurge(ctx context.Context, id, phase string) error {
	return s.mutate(ctx, func(state *State) error {
		key := "purge-" + id
		op, ok := state.PendingOperations[key]
		if !ok || op.Kind != "purge" {
			return ErrMutationConflict
		}
		if ClassifyPurge(*state, id) == PurgeInvalid {
			return ErrMutationConflict
		}
		if op.Phase == "committed" || op.Phase == phase || op.Phase == "content_removed" {
			return nil
		}
		if phase != "content_removed" || op.Phase != "tombstoned" {
			return ErrMutationConflict
		}
		op.Phase = phase
		state.PendingOperations[key] = op
		return nil
	})
}

func (s *Store) CompletePurge(ctx context.Context, id string) error {
	return s.mutate(ctx, func(state *State) error {
		key := "purge-" + id
		op := state.PendingOperations[key]
		switch ClassifyPurge(*state, id) {
		case PurgeCommitted:
			return nil
		case PurgeContentRemoved:
		default:
			return ErrMutationConflict
		}
		// Keep only the consumed topic identity in the existing purge receipt.
		// Canonical sessions and runtime-adopted sources may have no import
		// journal from which a future display reader could recover that identity.
		if topicID := state.Presentation[id].TopicID; topicID != "" {
			op.WorkspaceID, _ = sessionOwner(*state, id)
			if op.Presentation == nil {
				op.Presentation = &Presentation{TopicID: topicID}
			}
		}
		for key, workspace := range state.Workspaces {
			if slices.Contains(workspace.SessionIDs, id) {
				op.WorkspaceID = workspace.ID
			}
			workspace.SessionIDs = remove(workspace.SessionIDs, id)
			state.Workspaces[key] = workspace
		}
		// The content and presentation can go, but their topic ownership must
		// survive: otherwise a residual metadata row looks like a new topic.
		if topicID := state.Presentation[id].TopicID; topicID != "" {
			if op.Presentation == nil {
				op.Presentation = &Presentation{}
			}
			op.Presentation.TopicID = topicID
		}
		delete(state.Presentation, id)
		op.Phase, op.ResultGeneration = "committed", state.Generation+1
		state.PendingOperations[key] = op
		return nil
	})
}

func (s *Store) UpdatePresentation(ctx context.Context, ids []string, title *string, pinned *bool) error {
	return s.mutate(ctx, func(state *State) error {
		for _, id := range ids {
			if _, ok := sessionOwner(*state, id); !ok {
				return ErrSessionNotFound
			}
			value, exists := state.Presentation[id]
			if !exists {
				value.SortOrder = -1
			}
			if title != nil {
				value.Title = *title
			}
			if pinned != nil {
				value.Pinned = *pinned
			}
			state.Presentation[id] = value
		}
		return nil
	})
}

// EnsureSessionTopic publishes the initial display group without letting
// later tab rebuilds overwrite a user's persisted presentation.
func (s *Store) EnsureSessionTopic(ctx context.Context, id, topicID, title string) error {
	if id == "" || topicID == "" {
		return nil
	}
	return s.mutate(ctx, func(state *State) error {
		if _, ok := sessionOwner(*state, id); !ok {
			return ErrSessionNotFound
		}
		value := state.Presentation[id]
		if value.TopicID != "" {
			return nil
		}
		value.TopicID = topicID
		if value.Title == "" {
			value.Title = title
		}
		state.Presentation[id] = value
		return nil
	})
}

// unknownFields retains future, user-owned metadata during read/modify/write.
func unknownFields(body []byte, names ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	for _, name := range names {
		delete(fields, name)
	}
	return fields, nil
}
