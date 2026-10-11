package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/store"
)

func sessionWithTurns(t *testing.T, path string, turns int) *Session {
	t.Helper()
	s := NewSession("sys")
	for i := range turns {
		s.Add(provider.Message{Role: provider.RoleUser, Content: "prompt " + strings.Repeat("x", 8)})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: "reply"})
		if err := s.SaveSnapshot(path); err != nil {
			t.Fatalf("SaveSnapshot turn %d: %v", i, err)
		}
	}
	return s
}

func TestProbeSessionEventLogReadsOnlyNativeHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	logPath := store.SessionEventLog(path)
	largeRecord := `{"schema_version":2,"type":"message","messages":[{"role":"user","content":"` +
		strings.Repeat("x", int(sessionEventProbeMaxBytes*2)) + `"}]}` + "\n"
	if err := os.WriteFile(logPath, []byte(largeRecord), 0o600); err != nil {
		t.Fatalf("write event log: %v", err)
	}
	probe, err := probeSessionEventLogWithLimits(path, sessionReplayLimits{
		maxBytes: 128, maxRecords: 10, maxMessages: 10,
	})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !probe.dag || probe.size != int64(len(largeRecord)) {
		t.Fatalf("probe = %+v, want native size %d", probe, len(largeRecord))
	}
}

func TestDefaultSaveBootstrapsEventLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "one-shot"})
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(store.SessionEventLog(path)); err != nil {
		t.Fatalf("default save did not create an event log: %v", err)
	}
	if _, err := os.Stat(store.SessionEventIndex(path)); err != nil {
		t.Fatalf("default save did not create an event index: %v", err)
	}
}

func TestConcurrentLoadDuringAppendsStaysConsistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "seed"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("seed SaveSnapshot: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	errCh := make(chan error, 1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			loaded, err := LoadSession(path)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			if loaded.eventLogDamaged {
				select {
				case errCh <- os.ErrInvalid:
				default:
				}
				return
			}
		}
	}()
	for i := range 40 {
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("a", 512)})
		if err := s.SaveSnapshot(path); err != nil {
			t.Fatalf("SaveSnapshot %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-errCh:
		t.Fatalf("concurrent LoadSession failed or saw damage: %v", err)
	default:
	}
}

func TestLoadSessionUserMessagesSeesEventLogTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first prompt"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "reply"})
	s.Add(provider.Message{Role: provider.RoleUser, Content: "second prompt"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot append: %v", err)
	}

	users, err := LoadSessionUserMessages(path)
	if err != nil {
		t.Fatalf("LoadSessionUserMessages: %v", err)
	}
	if len(users) != 2 || users[0].Message.Content != "first prompt" || users[1].Message.Content != "second prompt" {
		t.Fatalf("user messages = %+v, want both prompts", users)
	}
	if users[1].At.IsZero() {
		t.Fatal("appended prompt lost its event timestamp")
	}
}

func TestLoadSessionUserMessagesDoesNotFallbackFromFutureEventSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	checkpoint := `{"role":"user","content":"older checkpoint prompt"}` + "\n"
	if err := os.WriteFile(path, []byte(checkpoint), 0o600); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
	events := `{"schema_version":99,"type":"replace","messages":[{"role":"user","content":"future prompt"}]}` + "\n"
	if err := os.WriteFile(store.SessionEventLog(path), []byte(events), 0o600); err != nil {
		t.Fatalf("write event log: %v", err)
	}

	users, err := LoadSessionUserMessages(path)
	if err == nil || !strings.Contains(err.Error(), "uses schema 99") {
		t.Fatalf("load error = %v, want future-schema refusal", err)
	}
	if users != nil {
		t.Fatalf("user messages = %+v, want no stale checkpoint fallback", users)
	}
}

func TestSessionEventLogPreservesUserCreatedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	want := time.Date(2026, 7, 16, 8, 30, 0, 0, time.UTC).UnixMilli()
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "timed prompt", CreatedAt: want})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	msgs := loaded.Snapshot()
	if len(msgs) != 2 || msgs[1].CreatedAt != want {
		t.Fatalf("loaded messages = %+v, want user createdAt %d", msgs, want)
	}
}

func TestSessionDigestIgnoresUserCreatedAt(t *testing.T) {
	withoutTime := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "prompt"},
	}
	withTime := append([]provider.Message(nil), withoutTime...)
	withTime[1].CreatedAt = 1_718_000_000_000

	withoutDigest, withoutSize, err := digestAndSizeSessionMessages(withoutTime)
	if err != nil {
		t.Fatalf("digest without createdAt: %v", err)
	}
	withDigest, withSize, err := digestAndSizeSessionMessages(withTime)
	if err != nil {
		t.Fatalf("digest with createdAt: %v", err)
	}
	if withoutDigest != withDigest || withoutSize != withSize {
		t.Fatalf("local createdAt changed transcript identity: digestEqual=%v size=%d/%d", withoutDigest == withDigest, withoutSize, withSize)
	}
	if !messagesHavePrefix(withTime, withoutTime) || !messagesHavePrefix(withoutTime, withTime) {
		t.Fatal("local createdAt broke cross-version transcript prefix matching")
	}
	if depth := messagesPrefixDigestDepth(withTime, withoutDigest); depth != len(withTime) {
		t.Fatalf("createdAt-aware prefix digest depth = %d, want %d", depth, len(withTime))
	}
}

func TestSessionContentModTimeTracksEventLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := sessionWithTurns(t, path, 1)
	s.Add(provider.Message{Role: provider.RoleUser, Content: "new"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	// Filesystems such as macOS can expose coarse mtime resolution, so the
	// checkpoint and event log written by one save are allowed to have the
	// same timestamp. Make the ordering under test explicit after the save:
	// the checkpoint is a stale anchor while the event log remains current.
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("age anchor: %v", err)
	}
	anchorInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat anchor: %v", err)
	}
	if got := SessionContentModTime(path); !got.After(anchorInfo.ModTime()) {
		t.Fatalf("SessionContentModTime = %v, want newer than stale anchor %v", got, anchorInfo.ModTime())
	}
}
