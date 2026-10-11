package workspacestate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ReadVersions tracks current canonical lifecycle generations.
type ReadVersions struct{ sessions map[string]string }

func readVersion(value any) string {
	body, _ := json.Marshal(value)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
func NewReadVersions(state State) *ReadVersions {
	v := &ReadVersions{sessions: map[string]string{}}
	for id, status := range state.SessionStates {
		v.sessions[id] = readVersion(status)
	}
	return v
}
func (v *ReadVersions) Session(id string) string    { return v.sessions[id] }
func (r *ReadSnapshot) ReadVersions() *ReadVersions { return r.versions }
