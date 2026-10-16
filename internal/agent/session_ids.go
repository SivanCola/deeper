package agent

import (
	"crypto/rand"
	"encoding/binary"
	"slices"
	"time"

	"deepzora/internal/provider"
)

// Message ids are 128 bits in Crockford base32 (26 chars, ULID layout for new
// ids). They never reach a provider and never enter transcript digests, so a
// history with or without ids hashes and serializes identically on the wire.
const (
	messageIDLen      = 26
	messageIDAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

// NewMessageID mints a time-prefixed random id: 48 bits of unix milliseconds
// followed by 80 random bits, so ids sort roughly by creation while staying
// unguessable across processes.
func NewMessageID() string {
	var raw [16]byte
	ms := uint64(time.Now().UnixMilli())
	binary.BigEndian.PutUint64(raw[:8], ms<<16)
	if _, err := rand.Read(raw[6:]); err != nil {
		binary.BigEndian.PutUint64(raw[8:], uint64(time.Now().UnixNano()))
	}
	return encodeMessageID(raw)
}

func encodeMessageID(raw [16]byte) string {
	var out [messageIDLen]byte
	hi := binary.BigEndian.Uint64(raw[:8])
	lo := binary.BigEndian.Uint64(raw[8:])
	for i := messageIDLen - 1; i >= 0; i-- {
		out[i] = messageIDAlphabet[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}

func mintMessageIDs(msgs []provider.Message) {
	for i := range msgs {
		if msgs[i].ID == "" {
			msgs[i].ID = NewMessageID()
		}
	}
}

// IndexOfID resolves a message id to its current position, or -1. It scans
// from the tail because callers almost always ask about recent messages.
func (s *Session) IndexOfID(id string) int {
	if s == nil || id == "" {
		return -1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, m := range slices.Backward(s.Messages) {
		if m.ID == id {
			return i
		}
	}
	return -1
}
