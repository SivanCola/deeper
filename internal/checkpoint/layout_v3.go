package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"deepzora/internal/fileutil"
	fileenc "deepzora/internal/fileutil/encoding"
)

func (s *Store) turnsDir() string {
	return filepath.Join(s.dir, "turns")
}

func (s *Store) turnDir(turn int) string {
	return filepath.Join(s.turnsDir(), strconv.Itoa(turn))
}

func (s *Store) v3MetaPath(turn int) string {
	return filepath.Join(s.turnDir(turn), "meta.json")
}

func (s *Store) v3BeforePath(turn, index int) string {
	return filepath.Join(s.turnDir(turn), "files", fmt.Sprintf("%04d.before", index))
}

func v3PayloadBytes(f FileSnap) ([]byte, error) {
	if f.rawContent != nil {
		return f.rawContent, nil
	}
	if f.Content == nil {
		return nil, nil
	}
	if f.Encoding != nil {
		return fileenc.Encode(*f.Content, *f.Encoding)
	}
	return []byte(*f.Content), nil
}

func (s *Store) persistV3(c *Checkpoint) error {
	turnDir := s.turnDir(c.Turn)
	if err := os.MkdirAll(filepath.Join(turnDir, "files"), 0o755); err != nil {
		return err
	}
	wire := *c
	wire.SchemaVersion = SchemaV3
	wire.Files = make([]FileSnap, len(c.Files))
	for i, f := range c.Files {
		snap := f
		payloadPath := s.v3BeforePath(c.Turn, i)
		if f.Content != nil && !f.PayloadExpired {
			payload, err := v3PayloadBytes(f)
			if err != nil {
				return err
			}
			if err := fileutil.AtomicWriteFile(payloadPath, payload, 0o644); err != nil {
				return err
			}
			snap.Content = nil
			snap.Encoding = nil
		} else if err := os.Remove(payloadPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		wire.Files[i] = snap
	}
	b, err := json.Marshal(&wire)
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(s.v3MetaPath(c.Turn), b, 0o644)
}

func (s *Store) removeTurnArtifacts(turns map[int]bool) error {
	if s.dir == "" {
		return nil
	}
	for turn := range turns {
		if err := os.RemoveAll(s.turnDir(turn)); err != nil {
			return fmt.Errorf("remove checkpoint turn %d: %w", turn, err)
		}
	}
	return nil
}

func (s *Store) loadV3Turns() []*Checkpoint {
	ents, err := os.ReadDir(s.turnsDir())
	if err != nil {
		return nil
	}
	var turns []*Checkpoint
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		turn, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := fileenc.ReadFileUTF8(s.v3MetaPath(turn))
		if err != nil {
			continue
		}
		var c Checkpoint
		if json.Unmarshal(b, &c) != nil || c.SchemaVersion != SchemaV3 {
			continue
		}
		c.Turn = turn
		for i := range c.Files {
			if c.Files[i].PayloadExpired {
				continue
			}
			raw, err := os.ReadFile(s.v3BeforePath(turn, i))
			if err != nil {
				continue
			}
			if want := c.Files[i].SHA256; want != "" && Digest(raw) != want {
				continue
			}
			enc, detected := fileenc.Detect(raw)
			text := string(fileenc.Decode(detected, enc))
			c.Files[i].Content = &text
			c.Files[i].Encoding = &enc
			c.Files[i].rawContent = append([]byte(nil), raw...)
		}
		turns = append(turns, &c)
	}
	return turns
}

func (s *Store) v3PayloadSize(turn int) (int64, error) {
	// Metadata also holds the bounded frozen result patches.
	root := s.turnDir(turn)
	var total int64
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}

// pruneV3TurnsLocked applies count retention and the payload
// payload budget to complete v3 turn directories. The current and protected
// turns may temporarily exceed the budget; the next unprotected turn prunes
// whole oldest directories.
func (s *Store) pruneV3TurnsLocked() {
	if s.dir == "" || s.retainN <= 0 {
		return
	}
	var turns []*Checkpoint
	for _, c := range s.all() {
		if c.SchemaVersion >= SchemaV3 {
			turns = append(turns, c)
		}
	}
	excess := len(turns) - s.retainN
	sizes := make(map[*Checkpoint]int64, len(turns))
	var totalSize int64
	sizeKnown := true
	for _, c := range turns {
		size, err := s.v3PayloadSize(c.Turn)
		if err != nil {
			sizeKnown = false
			break
		}
		sizes[c] = size
		totalSize += size
	}
	quotaExceeded := func() bool {
		return sizeKnown && s.blobQuota > 0 && totalSize > s.blobQuota
	}
	if excess <= 0 && !quotaExceeded() {
		return
	}
	removed := make(map[*Checkpoint]bool)
	for _, c := range turns {
		if excess <= 0 && !quotaExceeded() {
			break
		}
		if c == s.cur || s.protectTurns[c.Turn] {
			continue
		}
		if err := s.removeTurnArtifacts(map[int]bool{c.Turn: true}); err != nil {
			continue
		}
		removed[c] = true
		if excess > 0 {
			excess--
		}
		totalSize -= sizes[c]
	}
	if len(removed) == 0 {
		return
	}
	kept := s.done[:0]
	for _, c := range s.done {
		if !removed[c] {
			kept = append(kept, c)
		}
	}
	s.done = kept
	// Reclaim payloads no longer used by rewind transactions or undo.
	s.pruneBlobsLocked()
}
