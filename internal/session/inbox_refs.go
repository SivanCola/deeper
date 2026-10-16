package session

import (
	"context"
	"deepzora/internal/sessioncontent"
	"deepzora/internal/sessioninbox"
	"path/filepath"
)

// InboxDirectory keeps the instruction queue inside its owning session. Session
// deletion and export include it without a sibling sidecar or migration lookup.
func InboxDirectory(sessionDir string) string {
	return filepath.Join(sessionDir, "inbox")
}

func collectInboxContentRefs(ctx context.Context, sessionDir string) []sessioncontent.Ref {
	refs, _ := collectInboxContentRefsChecked(ctx, sessionDir)
	return refs
}

func collectInboxContentRefsChecked(ctx context.Context, sessionDir string) ([]sessioncontent.Ref, error) {
	dir := InboxDirectory(sessionDir)
	release, err := sessioninbox.FreezeSnapshot(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer release()
	return sessioninbox.FrozenContentRefs(dir)
}

//nolint:unused // The history export layer deduplicates its content closure with this key.
func contentRefKey(ref sessioncontent.Ref) string {
	return ref.Digest + ":" + itoa64(ref.Bytes) + ":" + ref.IndexDigest
}

//nolint:unused // Kept allocation-free for contentRefKey in the history export layer.
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
