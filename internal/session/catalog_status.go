package session

// CatalogState describes metadata problems observed by current readers. It
// never discovers sessions or replays history just to render host status.
type CatalogState struct {
	Revision  uint64
	Pending   int64
	Active    int64
	Failed    int64
	LastError string
}

type catalogObservation struct {
	status string
	err    string
}

func (q *Query) CatalogState() CatalogState {
	q.rebuildMu.Lock()
	defer q.rebuildMu.Unlock()
	state := CatalogState{Revision: q.catalogRevision}
	state.Active = int64(len(q.rebuilding) - len(q.metadataQueue))
	errorID := ""
	for id, observation := range q.catalogObserved {
		if observation.status == MetadataFailed {
			state.Failed++
			if errorID == "" || id < errorID {
				errorID, state.LastError = id, observation.err
			}
		} else {
			state.Pending++
		}
	}
	return state
}

// CatalogChanges coalesces metadata health changes for the owning host. The
// host owns cancellation; a Query never closes a channel a reader may signal.
func (q *Query) CatalogChanges() <-chan struct{} { return q.catalogChanges }

func (q *Query) observeCatalogInfo(info SessionInfo) {
	q.rebuildMu.Lock()
	defer q.rebuildMu.Unlock()
	q.observeCatalogLocked(info.SessionID, info.MetadataStatus, info.Error)
}

func (q *Query) observeCatalogLocked(id, status, message string) {
	if q.closed || id == "" {
		return
	}
	if message != "" {
		status = MetadataFailed
	}
	previous, exists := q.catalogObserved[id]
	if status == MetadataReady {
		if !exists {
			return
		}
		delete(q.catalogObserved, id)
	} else {
		next := catalogObservation{status: status, err: message}
		if exists && previous == next {
			return
		}
		q.catalogObserved[id] = next
	}
	q.catalogRevision++
	select {
	case q.catalogChanges <- struct{}{}:
	default:
	}
}
