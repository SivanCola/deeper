package session

import (
	"encoding/json"
	"os"
)

// The physical drain owns these witnesses. A new binding always republishes,
// and a failed or replaced publication remains eligible at the same sequence.
type durablePublication struct {
	sequence uint64
	file     os.FileInfo
}

func (p durablePublication) current(path string, sequence uint64) bool {
	if p.file == nil || p.sequence != sequence {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && os.SameFile(p.file, info) &&
		p.file.Size() == info.Size() && p.file.ModTime().Equal(info.ModTime())
}

func (p *durablePublication) record(path string, sequence uint64) {
	p.file = nil
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	// A background rebuild may replace the destination after our rename. Bind
	// the witness to the validated open file, never to a later path occupant.
	var publication struct {
		Sequence        *uint64 `json:"sequence"`
		DurableSequence *uint64 `json:"durableSequence"`
	}
	if err := json.NewDecoder(file).Decode(&publication); err != nil {
		return
	}
	if (publication.Sequence == nil || *publication.Sequence != sequence) &&
		(publication.DurableSequence == nil || *publication.DurableSequence != sequence) {
		return
	}
	after, err := file.Stat()
	if err != nil || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return
	}
	p.sequence, p.file = sequence, info
}
