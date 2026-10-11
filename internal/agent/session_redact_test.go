package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/store"
)

func TestRedactCurrentDAGPreservesBranchesAndErasesSupersededSecrets(t *testing.T) {
	path := dagTestSession(t)
	_, at := dagLinearLog(t, path)
	const secret = "sk-abcdefghijklmnopqrstuvwxyz12345678901234567890"
	patch, err := encodeSessionDAGMessage(dagMsg(provider.RoleAssistant, "public replacement", "M1"))
	if err != nil {
		t.Fatal(err)
	}
	dagAppend(t, path,
		dagMessageEntry(t, SessionMainHead, "U2", "", dagMsg(provider.RoleAssistant, secret, "M1"), at.Add(time.Minute)),
		sessionDAGEntry{Type: sessionDAGTypePatch, Head: SessionMainHead, Target: "M1", Msgs: patch, At: at.Add(2 * time.Minute)},
		sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "side", From: "U1", Kind: HeadKindFork, Name: secret, At: at.Add(3 * time.Minute)},
		dagMessageEntry(t, "side", "U1", "", dagMsg(provider.RoleAssistant, secret, "F1"), at.Add(4*time.Minute)),
		sessionDAGEntry{Type: sessionDAGTypeSelect, Head: "side", At: at.Add(5 * time.Minute)},
	)
	writer, err := AcquireSessionWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Release()
	before := dagReplay(t, path)
	logPath := store.SessionEventLog(path)
	original, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if changed, rewritten, err := RedactSessionLog(path, true); err != nil || !changed || rewritten != 0 {
		t.Fatalf("dry run = %v, %d, %v", changed, rewritten, err)
	}
	afterDryRun, _ := os.ReadFile(logPath)
	if !bytes.Equal(original, afterDryRun) {
		t.Fatal("dry run changed the log")
	}
	if changed, rewritten, err := RedactSessionLog(path, false); err != nil || !changed || rewritten <= 0 {
		t.Fatalf("redaction = %v, %d, %v", changed, rewritten, err)
	}
	after := dagReplay(t, path)
	if after.selectedHead() != before.selectedHead() || !reflect.DeepEqual(after.liveHeads(), before.liveHeads()) {
		t.Fatal("redaction changed branch selection or identities")
	}
	for _, head := range before.liveHeads() {
		oldMessages, _ := before.materialize(head)
		newMessages, _ := after.materialize(head)
		if len(oldMessages) != len(newMessages) || before.heads[head].leaf != after.heads[head].leaf {
			t.Fatalf("branch %s changed its chain", head)
		}
		for i := range oldMessages {
			if oldMessages[i].ID != newMessages[i].ID {
				t.Fatalf("branch %s message identity changed", head)
			}
		}
		encoded, _ := json.Marshal(newMessages)
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("branch %s retained secret", head)
		}
	}
	for _, artifact := range []string{path, logPath} {
		body, err := os.ReadFile(artifact)
		if err != nil || bytes.Contains(body, []byte(secret)) {
			t.Fatalf("secret retained in %s: %v", artifact, err)
		}
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	head, ok := loaded.Head()
	if !ok || head.HeadID != "side" {
		t.Fatalf("reopen selection = %v, %v", loaded, err)
	}
	if changed, _, err := RedactSessionLog(path, false); err != nil || changed {
		t.Fatalf("second redaction = %v, %v", changed, err)
	}
}
