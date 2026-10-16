// Package processidentity supplies the process identity used to address a
// live writer during session handoff. It does not read session files or leases.
package processidentity

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"time"
)

var writerID = NewWriterID()

// WriterID is stable for this process and distinct across process starts.
func WriterID() string { return writerID }

// NewWriterID creates an independent writer generation.
func NewWriterID() string {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "unknown-host"
	}
	host = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, host)
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Sprintf("%s-%d-%d", host, os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%d-%x", host, os.Getpid(), nonce[:])
}
