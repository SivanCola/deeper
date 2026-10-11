package doctor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/store"
)

func TestRedactSessionsScrubsCurrentSessionArtifacts(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-real-secret-value-123456"
	sessionPath := filepath.Join(dir, "abc.jsonl")
	if err := saveDoctorMessages(sessionPath, []string{`{"role":"tool","content":"DEEPSEEK_API_KEY=` + secret + `"}`}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		store.SessionGoalState(sessionPath):                             `{"schemaVersion":1,"goal":"rotate token ` + secret + `"}` + "\n",
		filepath.Join(store.SessionJobsDir(sessionPath), "bash-1.log"):  "DEEPSEEK_API_KEY=" + secret + "\n",
		filepath.Join(store.SessionJobsDir(sessionPath), "bash-1.json"): `{"label":"echo DEEPSEEK_API_KEY=` + secret + `"}` + "\n",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := agent.UpdateBranchMeta(sessionPath, false, func(m *agent.BranchMeta) error { m.Name = "DEEPSEEK_API_KEY=" + secret; return nil }); err != nil {
		t.Fatal(err)
	}

	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if len(res.Errors) > 0 {
		t.Fatalf("RedactSessions errors = %v", res.Errors)
	}
	if res.FilesChanged != 6 {
		t.Fatalf("FilesChanged = %d, want 6", res.FilesChanged)
	}
	for path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("%s still leaked secret:\n%s", path, data)
		}
	}
	// The rewrite must go through the real save machinery: the session still
	// loads, the event log still replays, and the masked value survived.
	loaded, err := agent.LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("redacted session no longer loads: %v", err)
	}
	if len(loaded.Messages) != 1 || !strings.Contains(loaded.Messages[0].Content, "DEEPSEEK_API_KEY=sk-rea") {
		t.Fatalf("redacted session lost its masked content: %+v", loaded.Messages)
	}
}

// TestRedactSessionsHandlesQuotedSecretsWithoutCorruption pins the decode-
// before-redact contract: on disk a quoted secret is JSON-encoded with \"
// escapes, and masking the raw bytes would eat the escape's backslash,
// truncate the JSON string, and leave the transcript undecodable — while the
// secret itself stayed in the clear.
func TestRedactSessionsHandlesQuotedSecretsWithoutCorruption(t *testing.T) {
	dir := t.TempDir()
	const secret = "hunter2-longer-secret-value"
	sessionPath := filepath.Join(dir, "abc.jsonl")
	line, err := json.Marshal(provider.Message{
		Role:    provider.RoleTool,
		Content: `export PASSWORD="` + secret + `"` + "\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := saveDoctorMessages(sessionPath, []string{string(line)}); err != nil {
		t.Fatal(err)
	}

	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if len(res.Errors) > 0 {
		t.Fatalf("RedactSessions errors = %v", res.Errors)
	}
	if res.FilesChanged != 2 {
		t.Fatalf("FilesChanged = %d, want 2", res.FilesChanged)
	}
	loaded, err := agent.LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("redaction corrupted the transcript: %v", err)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(loaded.Messages))
	}
	if strings.Contains(loaded.Messages[0].Content, secret) {
		t.Fatalf("quoted secret leaked: %q", loaded.Messages[0].Content)
	}
}

// TestRedactSessionsIsNoOpOnHealthyStore pins idempotence: after an explicit
// cleanup, rerunning the command must not rewrite or corrupt the clean store.
func TestRedactSessionsIsNoOpOnHealthyStore(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "abc.jsonl")
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "inspect"})
	s.Add(provider.Message{
		Role:       provider.RoleTool,
		Name:       "bash",
		ToolCallID: "call_1",
		Content:    `export PASSWORD="hunter2-longer-secret-value"` + "\n",
	})
	if err := s.Save(sessionPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if len(first.Errors) > 0 || first.FilesChanged == 0 {
		t.Fatalf("first RedactSessions() = %+v, want a successful rewrite", first)
	}
	before, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}

	second := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if len(second.Errors) > 0 {
		t.Fatalf("second RedactSessions errors = %v", second.Errors)
	}
	if second.FilesChanged != 0 {
		t.Fatalf("healthy already-redacted store rewritten: %+v", second)
	}
	after, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("healthy transcript bytes changed:\nbefore: %s\nafter:  %s", before, after)
	}
	if _, err := agent.LoadSession(sessionPath); err != nil {
		t.Fatalf("healthy session no longer loads: %v", err)
	}
}

func TestRedactSessionsDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-real-secret-value-123456"
	path := filepath.Join(dir, "abc.jsonl")
	body := `{"role":"tool","content":"DEEPSEEK_API_KEY=` + secret + `"}` + "\n"
	if err := saveDoctorMessages(path, []string{strings.TrimSpace(body)}); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}

	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}, DryRun: true})
	if res.FilesChanged != 2 {
		t.Fatalf("FilesChanged = %d, want 2", res.FilesChanged)
	}
	data, err := os.ReadFile(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(before) {
		t.Fatalf("dry-run modified file:\n%s", data)
	}
}

func TestRedactSessionsSkipsLeasedSession(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-real-secret-value-123456"
	path := filepath.Join(dir, "abc.jsonl")
	if err := saveDoctorMessages(path, []string{`{"role":"tool","content":"DEEPSEEK_API_KEY=` + secret + `"}`}); err != nil {
		t.Fatal(err)
	}
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer lease.Release()

	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if res.FilesSkipped != 3 {
		t.Fatalf("FilesSkipped = %d, want 3", res.FilesSkipped)
	}
	data, err := os.ReadFile(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), secret) {
		t.Fatalf("leased session should not be rewritten:\n%s", data)
	}
}

func TestRedactSessionsHoldsLeaseAcrossRewrite(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-" + "real-secret-value-123456"
	path := filepath.Join(dir, "abc.jsonl")
	if err := saveDoctorMessages(path, []string{`{"role":"tool","content":"DEEPSEEK_API_KEY=` + secret + `"}`}); err != nil {
		t.Fatal(err)
	}

	acquired := make(chan struct{})
	continueRedaction := make(chan struct{})
	sessionRedactionLeaseAcquired = func(got string) {
		if agent.CanonicalSessionPath(got) != agent.CanonicalSessionPath(path) {
			return
		}
		select {
		case <-acquired:
			return
		default:
			close(acquired)
		}
		<-continueRedaction
	}
	t.Cleanup(func() { sessionRedactionLeaseAcquired = nil })

	done := make(chan RedactSessionsResult, 1)
	go func() {
		done <- RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	}()
	<-acquired
	competing, err := agent.AcquireSessionWriter(path)
	if competing != nil {
		competing.Release()
	}
	if !errors.Is(err, agent.ErrSessionLeaseHeld) {
		close(continueRedaction)
		t.Fatalf("competing AcquireSessionWriter err = %v, want ErrSessionLeaseHeld", err)
	}
	close(continueRedaction)
	res := <-done
	if len(res.Errors) > 0 || res.FilesChanged != 2 {
		t.Fatalf("RedactSessions = %+v, want one successful DAG rewrite", res)
	}
}

// TestRedactSessionsRemovesDamagedSalvageSidecar pins the salvage-sidecar
// privacy gap (#6613 review): the .events.jsonl.damaged file preserves raw
// bytes tail repair truncated away, which can include secrets. The bytes are
// undecodable by definition, so no format-aware masking can prove them clean —
// the scrub must delete the file so no secret survives.
func TestRedactSessionsRemovesDamagedSalvageSidecar(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-real-secret-value-123456"
	sessionPath := filepath.Join(dir, "abc.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"clean"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	damagedPath := store.SessionEventLogDamaged(sessionPath)
	salvage := `{"damaged_tail":true,"preserved_at":"2026-01-01T00:00:00Z","log_offset":10,"bytes":80}` + "\n" +
		`{"schema_version":1,"type":"append","message_index":99,"messages":[{"role":"tool","content":"DEEPSEEK_API_KEY=` + secret + `"}]` + "\n"
	if err := os.WriteFile(damagedPath, []byte(salvage), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dry run reports the file without touching it.
	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}, DryRun: true})
	if len(res.Errors) > 0 {
		t.Fatalf("dry-run errors = %v", res.Errors)
	}
	if res.FilesChanged == 0 {
		t.Fatal("dry run did not report the damaged salvage sidecar")
	}
	if _, err := os.Stat(damagedPath); err != nil {
		t.Fatalf("dry run must not delete the sidecar: %v", err)
	}

	// The real run deletes it: no secret can survive in bytes we cannot parse.
	res = RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if len(res.Errors) > 0 {
		t.Fatalf("RedactSessions errors = %v", res.Errors)
	}
	if _, err := os.Stat(damagedPath); !os.IsNotExist(err) {
		data, _ := os.ReadFile(damagedPath)
		t.Fatalf("damaged salvage sidecar survived redaction (stat err=%v):\n%s", err, data)
	}
}

// TestRedactSessionsSkipsLeasedDamagedSalvage: like every other artifact, the
// salvage sidecar of a session another process is actively running must not
// be touched.
func TestRedactSessionsSkipsLeasedDamagedSalvage(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "abc.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"clean"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	damagedPath := store.SessionEventLogDamaged(sessionPath)
	if err := os.WriteFile(damagedPath, []byte("torn bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer lease.Release()

	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if res.FilesSkipped < 1 {
		t.Fatalf("FilesSkipped = %d, want >= 1", res.FilesSkipped)
	}
	if _, err := os.Stat(damagedPath); err != nil {
		t.Fatalf("leased session's salvage sidecar must survive: %v", err)
	}
}

// TestRedactSessionsScrubsStaleEventLogRecords pins the stale-record gap: a
// later replace event supersedes — but does not erase — earlier records, so a
// raw key can survive in an old event while the replayed view is already
// clean. Cleanup must compact the log anyway, and the replayed transcript
// (the clean current view) must be what survives.
func TestRedactSessionsScrubsStaleEventLogRecords(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-real-secret-value-123456"
	sessionPath := filepath.Join(dir, "abc.jsonl")
	if err := saveDoctorMessages(sessionPath, []string{`{"role":"tool","content":"DEEPSEEK_API_KEY=` + secret + `"}`}); err != nil {
		t.Fatal(err)
	}
	sess, err := agent.LoadSession(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	sess.Rewrite([]provider.Message{{Role: provider.RoleUser, Content: "clean"}}, "replace")
	if err := sess.SaveRewrite(sessionPath); err != nil {
		t.Fatal(err)
	}
	evPath := store.SessionEventLog(sessionPath)

	res := RedactSessions(RedactSessionsOptions{Dirs: []string{dir}})
	if len(res.Errors) > 0 {
		t.Fatalf("RedactSessions errors = %v", res.Errors)
	}
	if res.FilesChanged != 2 {
		t.Fatalf("FilesChanged = %d, want 2 (anchor + event log)", res.FilesChanged)
	}
	data, err := os.ReadFile(evPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("stale event log record still leaks secret:\n%s", data)
	}
	loaded, err := agent.LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("session no longer loads after compaction: %v", err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Content != "clean" {
		t.Fatalf("compaction lost the current replayed view: %+v", loaded.Messages)
	}
}
