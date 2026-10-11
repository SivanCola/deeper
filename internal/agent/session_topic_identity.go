package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// SessionTopicID identifies a current standalone session without writing metadata.
func SessionTopicID(path string) string {
	id := strings.TrimSpace(BranchID(path))
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	var b strings.Builder
	b.WriteString("session_")
	for _, r := range id {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	prefix := strings.TrimRight(b.String(), "_")
	if prefix == "session" {
		prefix = "session"
	}
	return prefix + "_" + hex.EncodeToString(sum[:])[:12]
}
