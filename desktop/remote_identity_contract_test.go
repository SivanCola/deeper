package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteIdentityContractRejectsRetiredSelectors(t *testing.T) {
	for _, body := range []string{`{"sessionName":"old"}`, `{"sessionPath":"session-id:old"}`, `{"sessionId":"current","sessionName":"old"}`} {
		var opts RemoteTabOpenOptions
		if err := json.Unmarshal([]byte(body), &opts); err == nil {
			t.Fatalf("accepted retired opening selector: %s", body)
		}
		var entry desktopRemoteTabEntry
		if err := json.Unmarshal([]byte(body), &entry); err == nil {
			t.Fatalf("accepted retired persisted selector: %s", body)
		}
	}
	if remoteSessionIdentityRoute("/old/session.jsonl", "") != "" || remoteSessionRoute(serveSessionEntry{Name: "old", Path: "session-id:old"}) != "" {
		t.Fatal("retired fields supplied a session identity")
	}
}

func TestRemoteListingRequiresUniqueExplicitSessionIDs(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`[{"name":"old","path":"session-id:old"}]`, false},
		{`[{"sessionId":"same"},{"sessionId":"same"}]`, false},
		{`[{"sessionId":" first "}]`, false},
		{`[{"sessionId":"first","name":"same","path":"/old"},{"sessionId":"second","name":"same","path":"/old"}]`, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
		rows, err := serveSessions(t.Context(), srv.Client(), srv.URL)
		srv.Close()
		if (err == nil) != tc.valid {
			t.Fatalf("listing %s: %v", tc.body, err)
		}
		if tc.valid && (rows[0].SessionID != "first" || rows[1].SessionID != "second" || remoteSessionRoute(rows[0]) != "session-id:first") {
			t.Fatalf("wrong identity: %+v", rows)
		}
	}
}

func TestRemoteStatusUsesCurrentIdentityInsteadOfRetiredName(t *testing.T) {
	for _, tc := range []struct {
		body string
		id   string
	}{
		{`{"sessionName":"old","sessionPath":"/old/session.jsonl"}`, ""},
		{`{"sessionName":"old","sessionId":"current"}`, "current"},
		{`{"sessionPath":"session-id:current"}`, "current"},
	} {
		var fields map[string]any
		if err := json.Unmarshal([]byte(tc.body), &fields); err != nil {
			t.Fatal(err)
		}
		fields["runtimeState"] = remoteRuntimeTestSnapshot("identity-test", 1, "idle")
		var payload remoteTabStatusPayload
		if err := json.Unmarshal([]byte(remoteRuntimeTestJSON(t, fields)), &payload); err != nil {
			t.Fatal(err)
		}
		tab := &remoteTab{session: remoteTabSessionState{reset: true, newSession: true}}
		applyRemoteTabStatusPayload(tab, payload)
		if tab.session.name != tc.id || tab.session.reset != (tc.id == "") || tab.session.newSession != (tc.id == "") {
			t.Fatalf("status %s changed identity through a retired field: %+v", tc.body, tab.session)
		}
	}
}
