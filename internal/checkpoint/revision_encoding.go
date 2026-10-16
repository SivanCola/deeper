package checkpoint

import (
	fileenc "deepzora/internal/fileutil/encoding"
	"fmt"
)

// encodeRevisionContent restores the captured encoding, independent of later
// workspace edits. A textual preview without an explicit encoding is UTF-8.
func encodeRevisionContent(rev FileRevision) ([]byte, error) {
	if rev.Content == nil {
		return nil, fmt.Errorf("missing payload for %s", rev.Path)
	}
	enc := fileenc.UTF8
	if rev.Encoding != nil {
		enc = *rev.Encoding
	}
	return fileenc.Encode(*rev.Content, enc)
}
