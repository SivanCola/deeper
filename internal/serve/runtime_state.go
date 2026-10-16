package serve

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"sync"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
)

type runtimeSessionView struct {
	SessionPath string                     `json:"sessionPath"`
	Current     bool                       `json:"current"`
	State       event.RuntimeStateSnapshot `json:"state"`
}

type runtimeStatesView struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Epoch         string               `json:"epoch"`
	Revision      uint64               `json:"revision"`
	Sessions      []runtimeSessionView `json:"sessions"`
}
type serveRuntimeProjection struct {
	mu       sync.Mutex
	snapshot runtimeStatesView
}

func runtimeStateOf(ctrl control.SessionAPI) event.RuntimeStateSnapshot {
	if ctrl != nil {
		return ctrl.RuntimeStateSnapshot()
	}
	return event.RuntimeStateSnapshot{SchemaVersion: 1, Phase: "unknown"}
}

// runtimeStates is a memory-only reconciliation surface, including detached
// controllers. It does not list transcripts, generate titles, or fetch balance.
func (s *Server) runtimeStates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.runtimeStatesSnapshot())
}

func (s *Server) runtimeStatesSnapshot() runtimeStatesView {
	r := &s.runtimeProjection
	r.mu.Lock()
	defer r.mu.Unlock()
	s.bindMu.Lock()
	current := s.ctl()
	controllers := []control.SessionAPI{current}
	s.detachedMu.Lock()
	for _, detached := range s.detached {
		if detached.ctrl != current {
			controllers = append(controllers, detached.ctrl)
		}
	}
	s.detachedMu.Unlock()
	result := runtimeStatesView{SchemaVersion: 1, Epoch: r.snapshot.Epoch, Revision: r.snapshot.Revision, Sessions: []runtimeSessionView{}}
	for _, ctrl := range controllers {
		if ctrl == nil {
			continue
		}
		path := sessionRouteForController(ctrl)
		result.Sessions = append(result.Sessions, runtimeSessionView{SessionPath: path, Current: ctrl == current, State: runtimeStateOf(ctrl)})
	}
	s.bindMu.Unlock()
	sort.Slice(result.Sessions, func(i, j int) bool {
		return result.Sessions[i].State.RuntimeEpoch < result.Sessions[j].State.RuntimeEpoch
	})
	if result.Epoch == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			panic(err)
		}
		result.Epoch = hex.EncodeToString(id[:])
	}
	if !reflect.DeepEqual(result, r.snapshot) {
		result.Revision++
		r.snapshot = result
	}
	result.Sessions = append([]runtimeSessionView{}, r.snapshot.Sessions...)
	return result
}

func (b *Broadcaster) RuntimeStateChanged(snapshot event.RuntimeStateSnapshot) {
	b.publishRuntimeState(b.CurrentSession(), snapshot)
}
func (b *Broadcaster) publishRuntimeState(path string, snapshot event.RuntimeStateSnapshot) {
	// Runtime frames need the same identity route as content frames and GET.
	// Canonical sessions have no legacy transcript path.
	if snapshot.SessionID != "" {
		path = remoteSessionIDQueryPrefix + snapshot.SessionID
	}
	path = sessionRouteKey(path)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.modelApplicationChanged != nil {
		b.modelApplicationChanged()
	}
	frame, err := json.Marshal(eventwire.Event{Kind: "runtime_state", SessionID: snapshot.SessionID, SessionPath: path, SessionCurrent: path == b.current, RuntimeState: &snapshot})
	if err != nil {
		return
	}
	for ch := range b.subs {
		enqueueSubscriberWireFrame(ch, frame, "runtime_state")
	}
}

func (s *sessionTagSink) RuntimeStateChanged(snapshot event.RuntimeStateSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active || !s.runtimeActive {
		s.pendingRuntimeState = &snapshot
		return
	}
	s.bc.publishRuntimeState(s.routeLocked(), snapshot)
}
