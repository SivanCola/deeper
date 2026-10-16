package main

import (
	"strings"
	"testing"
)

func TestSessionRuntimeKeyUsesOnlyCurrentIdentity(t *testing.T) {
	for _, raw := range []string{"session-id:current", "session-id:CaseMix", " session-id:current "} {
		key := sessionRuntimeKey(raw)
		if key != strings.TrimSpace(raw) || sessionRuntimeKey(key) != key {
			t.Fatalf("current route key=%q for %q", key, raw)
		}
	}
	for _, raw := range []string{"/sessions/history.jsonl", "history.jsonl", "session-id:bad/id", "session-id:"} {
		if key := sessionRuntimeKey(raw); key != "" {
			t.Fatalf("non-current identity %q accepted as %q", raw, key)
		}
	}
	if sessionRuntimeKey("session-id:CaseMix") == sessionRuntimeKey("session-id:casemix") {
		t.Fatal("opaque session IDs were case-folded")
	}
}

func TestSessionLeaseBusyErrorOmitsSettingClause(t *testing.T) {
	generic := (&sessionLeaseBusyError{}).Error()
	if strings.Contains(generic, "before changing") {
		t.Fatalf("empty-setting busy error still names a setting: %q", generic)
	}
	withSetting := (&sessionLeaseBusyError{setting: "model"}).Error()
	if !strings.Contains(withSetting, "before changing model") {
		t.Fatalf("setting busy error lost its clause: %q", withSetting)
	}
}

func TestSessionRouteFenceRejectsFileAliasesAndMissingIdentity(t *testing.T) {
	for _, pair := range [][2]string{{"", ""}, {"same.jsonl", "same.jsonl"}, {"session-id:a/b", "session-id:a/b"}, {"session-id:CaseMix", "session-id:casemix"}} {
		if sameSessionRoute(pair[0], pair[1]) {
			t.Fatalf("invalid route fence matched %q and %q", pair[0], pair[1])
		}
	}
	if !sameSessionRoute("session-id:current", " session-id:current ") {
		t.Fatal("current route spelling did not match its identity")
	}
}
