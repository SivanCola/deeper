package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"deepzora/internal/agent"
	"deepzora/internal/fileutil"
	"deepzora/internal/secrets"
	"deepzora/internal/store"
)

// RedactSessionsOptions controls historical session-log redaction.
type RedactSessionsOptions struct {
	Dirs   []string
	DryRun bool
}

// RedactSessionsResult summarizes a historical session-log redaction run.
type RedactSessionsResult struct {
	Dirs           []string `json:"dirs"`
	FilesScanned   int64    `json:"files_scanned"`
	FilesChanged   int64    `json:"files_changed"`
	FilesSkipped   int64    `json:"files_skipped"`
	BytesRewritten int64    `json:"bytes_rewritten"`
	DryRun         bool     `json:"dry_run"`
	Errors         []string `json:"errors,omitempty"`
}

// RedactSessions masks credential-shaped values already persisted in Deepzora
// session transcripts, event logs, branch metadata, goal state, and
// background-job artifacts. It is intentionally scoped to known Deepzora
// session directories; it is not a general-purpose filesystem scrubber.
//
// Every JSON-bearing artifact is decoded before masking and re-encoded after:
// running Redact over raw encoded bytes would eat the backslash of a \" escape
// whenever a secret-shaped value abuts a quote, truncating the JSON string and
// leaving the transcript undecodable (and the secret unmasked). Only plain-text
// job logs are redacted as raw bytes.
func RedactSessions(opts RedactSessionsOptions) RedactSessionsResult {
	dirs := redactSessionDirs(opts.Dirs)
	res := RedactSessionsResult{Dirs: dirs, DryRun: opts.DryRun}
	for _, dir := range dirs {
		var candidates []string
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", path, err))
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || !redactSessionCandidate(path) {
				return nil
			}
			candidates = append(candidates, path)
			return nil
		}); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", dir, err))
		}
		// A transcript rewrite now refreshes its listing projection. Process an
		// existing branch-meta sidecar first so a secret-bearing sidecar is
		// counted and redacted before the transcript replaces its preview.
		sort.SliceStable(candidates, func(i, j int) bool {
			left, right := redactionCandidatePriority(candidates[i]), redactionCandidatePriority(candidates[j])
			if left != right {
				return left < right
			}
			return candidates[i] < candidates[j]
		})
		for _, path := range candidates {
			res.FilesScanned++
			sessionPath := redactionSessionPath(path)
			writers, err := acquireSessionRedactionWriters(sessionPath)
			if err != nil {
				if errors.Is(err, agent.ErrSessionLeaseHeld) {
					res.FilesSkipped++
				} else {
					res.Errors = append(res.Errors, fmt.Sprintf("%s: acquire session lease: %v", path, err))
				}
				continue
			}
			changed, rewritten, err := func() (int64, int64, error) {
				defer releaseSessionRedactionWriters(writers)
				if sessionRedactionLeaseAcquired != nil {
					sessionRedactionLeaseAcquired(sessionPath)
				}
				return redactSessionArtifact(path, opts.DryRun)
			}()
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			res.FilesChanged += changed
			res.BytesRewritten += rewritten
		}
	}
	return res
}

func redactionCandidatePriority(path string) int {
	if strings.HasSuffix(filepath.Base(path), ".jsonl.meta") {
		return 0
	}
	if store.IsSessionTranscriptName(filepath.Base(path)) {
		return 1
	}
	return 2
}

func redactSessionDirs(in []string) []string {
	var candidates []string
	if len(in) > 0 {
		candidates = append(candidates, in...)
	} else {
		candidates = append(candidates, sessionBundleSearchDirs()...)
	}
	seen := map[string]bool{}
	var out []string
	for _, dir := range candidates {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func redactSessionCandidate(path string) bool {
	name := filepath.Base(path)
	switch {
	case store.IsSessionTranscriptName(name):
		return true
	case strings.HasSuffix(name, ".jsonl.meta"):
		return true
	case strings.HasSuffix(name, ".events.jsonl"):
		return true
	case strings.HasSuffix(name, ".events.jsonl.damaged"):
		return true
	case strings.HasSuffix(name, ".guardian.jsonl"):
		return true
	case strings.HasSuffix(name, ".goal-state.json"):
		return true
	case filepath.Base(filepath.Dir(path)) != "" && strings.HasSuffix(filepath.Base(filepath.Dir(path)), ".jobs"):
		return strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".json")
	default:
		return false
	}
}

func redactionSessionPath(path string) string {
	name := filepath.Base(path)
	switch {
	case store.IsSessionTranscriptName(name), strings.HasSuffix(name, ".guardian.jsonl"):
		return path
	case strings.HasSuffix(path, ".jsonl.meta"):
		return strings.TrimSuffix(path, ".meta")
	case strings.HasSuffix(path, ".events.jsonl.damaged"):
		return strings.TrimSuffix(path, ".events.jsonl.damaged") + ".jsonl"
	case strings.HasSuffix(path, ".events.jsonl"):
		return strings.TrimSuffix(path, ".events.jsonl") + ".jsonl"
	case strings.HasSuffix(path, ".goal-state.json"):
		return strings.TrimSuffix(path, ".goal-state.json") + ".jsonl"
	case strings.HasSuffix(filepath.Base(filepath.Dir(path)), ".jobs"):
		return strings.TrimSuffix(filepath.Dir(path), ".jobs") + ".jsonl"
	default:
		return ""
	}
}

var sessionRedactionLeaseAcquired func(string)

func acquireSessionRedactionWriters(sessionPath string) ([]*agent.SessionWriter, error) {
	if strings.TrimSpace(sessionPath) == "" {
		return nil, nil
	}
	paths := []string{agent.CanonicalSessionPath(sessionPath)}
	if before, ok := strings.CutSuffix(sessionPath, ".guardian.jsonl"); ok {
		paths = append(paths, agent.CanonicalSessionPath(before+".jsonl"))
	}
	sort.Strings(paths)
	writers := make([]*agent.SessionWriter, 0, len(paths))
	for i, path := range paths {
		if i > 0 && path == paths[i-1] {
			continue
		}
		writer, err := agent.AcquireSessionWriter(path)
		if err != nil {
			releaseSessionRedactionWriters(writers)
			return nil, err
		}
		writers = append(writers, writer)
	}
	return writers, nil
}

func releaseSessionRedactionWriters(writers []*agent.SessionWriter) {
	for _, writer := range slices.Backward(writers) {
		writer.Release()
	}
}

// redactSessionArtifact dispatches one candidate file to a format-aware
// redactor and reports how many files it changed.
func redactSessionArtifact(path string, dryRun bool) (changed int64, bytesRewritten int64, err error) {
	name := filepath.Base(path)
	switch {
	case store.IsSessionTranscriptName(name), strings.HasSuffix(name, ".guardian.jsonl"):
		return redactSessionTranscript(path, dryRun)
	case strings.HasSuffix(name, ".events.jsonl.damaged"):
		// The salvage sidecar holds raw bytes tail repair truncated away —
		// undecodable by definition, so format-aware masking is impossible,
		// and raw-byte masking cannot guarantee a secret split by JSON
		// escapes is even recognized. This explicit privacy scrub follows the
		// event-log precedent (torn bytes are compacted away regardless of
		// content): delete the sidecar outright. Privacy wins over forensics.
		return removeDamagedSalvage(path, dryRun)
	case strings.HasSuffix(name, ".events.jsonl"):
		anchor := strings.TrimSuffix(path, ".events.jsonl") + ".jsonl"
		if _, statErr := os.Stat(anchor); statErr == nil {
			// The anchor's own walk entry rewrites the event log with it.
			return 0, 0, nil
		}
		return redactSessionTranscript(anchor, dryRun)
	case strings.HasSuffix(name, ".jsonl.meta"):
		return redactBranchMeta(strings.TrimSuffix(path, ".meta"), dryRun)
	case strings.HasSuffix(name, ".goal-state.json"):
		return redactJSONFile(path, dryRun)
	case strings.HasSuffix(name, ".json"):
		return redactJSONFile(path, dryRun)
	default:
		// Background-job .log files are plain text: raw-byte redaction is
		// correct there and only there.
		return redactPlainTextFile(path, dryRun)
	}
}

// redactBranchMeta masks the free-text fields of the branch-metadata sidecar
// (preview, titles, goal, recovery reason) through the typed load/save pair so
// revisions, digests, and timestamps survive untouched.
func redactBranchMeta(sessionPath string, dryRun bool) (int64, int64, error) {
	unlock, err := agent.LockSessionMetaPath(sessionPath)
	if err != nil {
		return 0, 0, err
	}
	defer unlock()
	meta, ok, err := agent.LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		return 0, 0, err
	}
	changed := false
	for _, field := range []*string{&meta.Name, &meta.TopicTitle, &meta.CustomTitle, &meta.Goal, &meta.Preview, &meta.RecoveryReason} {
		if masked := secrets.Redact(*field); masked != *field {
			*field = masked
			changed = true
		}
	}
	if !changed {
		return 0, 0, nil
	}
	if dryRun {
		return 1, 0, nil
	}
	if err := agent.SaveBranchMetaPreserveUpdatedLocked(sessionPath, meta); err != nil {
		return 0, 0, err
	}
	var rewritten int64
	if info, err := os.Stat(agent.BranchMetaPath(sessionPath)); err == nil {
		rewritten = info.Size()
	}
	return 1, rewritten, nil
}

// redactJSONFile decodes a single-document JSON sidecar, masks every string
// value in the tree, and re-encodes. UseNumber keeps numeric literals (large
// IDs, timestamps) byte-faithful through the round trip. A file that does not
// parse is reported and left untouched rather than risked with a raw rewrite.
func redactJSONFile(path string, dryRun bool) (int64, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return 0, 0, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return 0, 0, fmt.Errorf("not valid JSON, left untouched: %w", err)
	}
	doc, changed := redactJSONValue(doc)
	if !changed {
		return 0, 0, nil
	}
	next, err := json.Marshal(doc)
	if err != nil {
		return 0, 0, err
	}
	next = append(next, '\n')
	if dryRun {
		return 1, int64(len(next)), nil
	}
	perm := info.Mode().Perm()
	if perm == 0 {
		perm = 0o600
	}
	if err := fileutil.AtomicWriteFile(path, next, perm); err != nil {
		return 0, 0, err
	}
	return 1, int64(len(next)), nil
}

// redactSessionTranscript handles only the current DAG format.
func redactSessionTranscript(path string, dryRun bool) (int64, int64, error) {
	changed, rewritten, err := agent.RedactSessionLog(path, dryRun)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if !changed {
		return 0, 0, nil
	}
	return 2, rewritten, nil
}

func redactJSONValue(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		masked := secrets.Redact(t)
		return masked, masked != t
	case map[string]any:
		changed := false
		for key, val := range t {
			next, ch := redactJSONValue(val)
			if ch {
				t[key] = next
				changed = true
			}
		}
		return t, changed
	case []any:
		changed := false
		for i, val := range t {
			next, ch := redactJSONValue(val)
			if ch {
				t[i] = next
				changed = true
			}
		}
		return t, changed
	default:
		return v, false
	}
}

// removeDamagedSalvage deletes an .events.jsonl.damaged salvage sidecar. See
// the dispatch comment: damaged bytes cannot be masked reliably, so the scrub
// removes them entirely.
func removeDamagedSalvage(path string, dryRun bool) (int64, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	if info.IsDir() {
		return 0, 0, nil
	}
	if dryRun {
		return 1, 0, nil
	}
	if err := os.Remove(path); err != nil {
		return 0, 0, err
	}
	return 1, 0, nil
}

// redactPlainTextFile masks raw bytes — safe only for non-JSON artifacts
// (background-job .log output).
func redactPlainTextFile(path string, dryRun bool) (int64, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	if info.IsDir() {
		return 0, 0, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	next := []byte(secrets.Redact(string(raw)))
	if bytes.Equal(raw, next) {
		return 0, 0, nil
	}
	if dryRun {
		return 1, int64(len(next)), nil
	}
	perm := info.Mode().Perm()
	if perm == 0 {
		perm = 0o600
	}
	if err := fileutil.AtomicWriteFile(path, next, perm); err != nil {
		return 0, 0, err
	}
	return 1, int64(len(next)), nil
}
