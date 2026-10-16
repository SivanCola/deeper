package main

import (
	"fmt"
	"reflect"
	"testing"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestColdHistoryPagesRemainReachableInBothDirections(t *testing.T) {
	var messages []provider.Message
	for i := range 20 {
		messages = append(messages, currentHistoryUser(i, fmt.Sprintf("question %d", i)), currentHistoryAssistant(i, fmt.Sprintf("answer %d", i)))
	}
	a, tab := currentBoundHistoryFixture(t, messages)
	h, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer a.ReleaseSessionHistoryRead(h.ID)
	request := session.HistoryWindowRequest{Anchor: "newest", Limit: 4}
	newest := currentBoundWindow(t, a, h.ID, request)
	if newest.Status != "ready" || !newest.HasOlder {
		t.Fatalf("newest: %+v %v", newest, err)
	}
	request.Anchor, request.Cursor = "cursor", newest.OlderCursor
	older := currentBoundWindow(t, a, h.ID, request)
	if older.Status != "ready" || !older.HasNewer {
		t.Fatalf("older: %+v %v", older, err)
	}
	request.Cursor, request.Direction = older.NewerCursor, "newer"
	back := currentBoundWindow(t, a, h.ID, request)
	if back.Status != "ready" {
		t.Fatalf("return newer: %+v %v", back, err)
	}
	if !reflect.DeepEqual(back.Messages, newest.Messages) {
		t.Fatal("reclaimed newer page cannot be reached exactly")
	}
}
