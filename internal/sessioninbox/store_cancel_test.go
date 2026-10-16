package sessioninbox

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDiscardPendingItemsOwnedResultReturnsOnlyWithdrawnIDs(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "inbox")
	s, err := OpenAt("session-id:s", session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	queued, _ := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "queued"}, Source: "desktop"})
	accepted, _ := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "accepted"}, Source: "desktop"})
	consumed, _ := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "consumed"}, Source: "desktop"})
	foreign, _ := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "foreign"}, Source: "bot"})
	if err := s.SetState(accepted.ItemID, StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(consumed.ItemID, StateSteerConsumed, ""); err != nil {
		t.Fatal(err)
	}

	discarded, err := s.DiscardPendingItemsOwnedResult(
		[]string{queued.ItemID, accepted.ItemID, consumed.ItemID, foreign.ItemID, "missing"},
		"desktop",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(discarded) != 2 || discarded[0] != queued.ItemID || discarded[1] != accepted.ItemID {
		t.Fatalf("discarded ids = %v", discarded)
	}
	items := s.Snapshot().Items
	if len(items) != 2 || items[0].ID != consumed.ItemID || items[1].ID != foreign.ItemID {
		t.Fatalf("remaining items = %+v", items)
	}
}

func TestMarkSteerConsumedCompareAndTransition(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "inbox")
	s, err := OpenAt("session-id:s", session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, _ := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "steer"}})
	if err := s.MarkSteerConsumed(rec.ItemID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("queued transition error = %v, want ErrInvalidState", err)
	}
	if err := s.SetState(rec.ItemID, StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSteerConsumed(rec.ItemID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSteerConsumed(rec.ItemID); err != nil {
		t.Fatalf("idempotent transition failed: %v", err)
	}
	if got := s.Snapshot().Items[0].State; got != StateSteerConsumed {
		t.Fatalf("state = %q, want %q", got, StateSteerConsumed)
	}
}

func TestDeletePendingOrAcceptedItemLinearizesWithSteerConsumption(t *testing.T) {
	newAccepted := func(t *testing.T) (*Store, string) {
		t.Helper()
		s, err := OpenAt("session-id:s", filepath.Join(t.TempDir(), "inbox"), Limits{})
		if err != nil {
			t.Fatal(err)
		}
		rec, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "steer"}})
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		if err := s.SetState(rec.ItemID, StateSteerAccepted, ""); err != nil {
			s.Close()
			t.Fatal(err)
		}
		return s, rec.ItemID
	}

	t.Run("delete wins", func(t *testing.T) {
		s, id := newAccepted(t)
		defer s.Close()
		if err := s.DeletePendingOrAcceptedItem(id); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkSteerConsumed(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("consume after delete = %v, want ErrNotFound", err)
		}
	})

	t.Run("consume wins", func(t *testing.T) {
		s, id := newAccepted(t)
		defer s.Close()
		if err := s.MarkSteerConsumed(id); err != nil {
			t.Fatal(err)
		}
		if err := s.DeletePendingOrAcceptedItem(id); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("delete after consume = %v, want ErrInvalidState", err)
		}
		meta, _, err := s.ReadItem(id)
		if err != nil {
			t.Fatal(err)
		}
		if meta.State != StateSteerConsumed {
			t.Fatalf("state = %q, want %q", meta.State, StateSteerConsumed)
		}
	})
}
