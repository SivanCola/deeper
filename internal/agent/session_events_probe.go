package agent

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"deepzora/internal/store"
)

// The current DAG codec owns only logs declaring its supported schema.
type sessionEventLogProbe struct {
	size          int64
	creatable     bool // missing/empty; a new DAG log may be created
	dag           bool // first record is a schema-2 DAG entry
	futureSchema  bool // first record declares a newer schema than this build
	schemaVersion int
}

// Reject filenames whose current durable sidecars exceed filesystem limits.
func sessionEventSidecarsFit(sessionPath string) bool {
	logName := filepath.Base(store.SessionEventLog(sessionPath))
	indexName := filepath.Base(store.SessionEventIndex(sessionPath))
	return len(logName) <= nameMaxBytes && len(indexName) <= nameMaxBytes
}

// probeSessionEventLog inspects the first record of the event log to decide
// whether the native persistence layer owns the file. Missing or empty logs
// count as native (we may create/append); an undecodable or foreign first
// record — or a transcript name too long for the sidecars to fit — marks the
// file as not ours.
func probeSessionEventLog(sessionPath string) (sessionEventLogProbe, error) {
	return probeSessionEventLogWithLimits(sessionPath, defaultSessionReplayLimits)
}

func probeSessionEventLogWithLimits(sessionPath string, _ sessionReplayLimits) (sessionEventLogProbe, error) {
	path := store.SessionEventLog(sessionPath)
	if path == "" {
		return sessionEventLogProbe{creatable: true}, nil
	}
	if !sessionEventSidecarsFit(sessionPath) {
		return sessionEventLogProbe{}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sessionEventLogProbe{creatable: true}, nil
		}
		return sessionEventLogProbe{}, err
	}
	if info.IsDir() {
		return sessionEventLogProbe{}, nil
	}
	if info.Size() == 0 {
		return sessionEventLogProbe{creatable: true}, nil
	}
	probe := sessionEventLogProbe{size: info.Size()}
	f, err := os.Open(path)
	if err != nil {
		return sessionEventLogProbe{}, err
	}
	defer f.Close()
	var schemaVersion int
	var ok bool
	schemaVersion, _, ok = probeSessionEventHeader(f)

	if !ok {
		// Nothing decodable at the head: not a native log this build can own.
		return probe, nil
	}
	probe.schemaVersion = schemaVersion
	switch {
	case schemaVersion == sessionDAGSchemaVersion:
		probe.dag = true
	case schemaVersion > sessionDAGSchemaVersion:
		// A newer writer owns this log; ignoring or truncating it would
		// silently discard that writer's transcript.
		probe.futureSchema = true
	}
	return probe, nil
}

// probeSessionEventHeader searches a bounded prefix for the identifying fields.
// Using Decode on a partial struct still buffers the whole JSON value, so native
// writer output must take this fast path before replay's byte budget is checked.
func probeSessionEventHeader(r io.Reader) (schemaVersion int, eventType string, ok bool) {
	dec := json.NewDecoder(io.LimitReader(r, sessionEventProbeMaxBytes))
	tok, err := dec.Token()
	if err != nil {
		return 0, "", false
	}
	if delim, isDelim := tok.(json.Delim); !isDelim || delim != '{' {
		return 0, "", false
	}
	var haveSchema, haveType bool
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return 0, "", false
		}
		name, isString := key.(string)
		if !isString {
			return 0, "", false
		}
		switch name {
		case "schema_version":
			if err := dec.Decode(&schemaVersion); err != nil {
				return 0, "", false
			}
			haveSchema = true
		case "type":
			if err := dec.Decode(&eventType); err != nil {
				return 0, "", false
			}
			haveType = true
		default:
			var discard json.RawMessage
			if err := dec.Decode(&discard); err != nil {
				return 0, "", false
			}
		}
		if haveSchema && haveType {
			return schemaVersion, eventType, true
		}
	}
	return 0, "", false
}
