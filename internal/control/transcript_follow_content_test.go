package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deepzora/internal/agent/testutil"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/transcript"
)

func TestTranscriptFollowKeepsOversizedBodyReadable(t *testing.T) {
	for name, body := range map[string]string{"plain": strings.Repeat("x", 2<<20), "escaped": strings.Repeat("<", 400000)} {
		t.Run(name, func(t *testing.T) {
			c, _, runtime := newTranscriptBoundaryController(t, testutil.Turn{}, event.Discard)
			payload, err := json.Marshal(map[string]any{"message": provider.Message{ID: "large", Role: provider.RoleAssistant, Content: body}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Session().Append(t.Context(), session.Batch{OperationID: "large-body", Events: []session.Event{{Kind: "message/complete", Payload: payload}}}); err != nil {
				t.Fatal(err)
			}
			response, err := c.TranscriptFollow(t.Context(), transcript.FollowRequest{})
			if err != nil {
				t.Fatal(err)
			}
			defer c.TranscriptFollow(context.Background(), transcript.FollowRequest{Subscription: response.Subscription, Close: true})
			encoded, err := json.Marshal(response)
			if err != nil || len(encoded)+1 > transcript.MaxResponseBytes || response.Snapshot == nil || len(response.Snapshot.Records) != 1 || len(response.Snapshot.Records[0].Refs) != 1 {
				t.Fatalf("unbounded or unreadable follow: bytes=%d err=%v", len(encoded), err)
			}
			ref := response.Snapshot.Records[0].Refs[0]
			var full strings.Builder
			for offset := 0; ; {
				chunk, err := c.TranscriptContent(transcript.ContentRequest{ContentRef: ref, Offset: offset})
				if err != nil || chunk.Stale {
					t.Fatalf("content: stale=%v err=%v", chunk.Stale, err)
				}
				full.WriteString(chunk.Data)
				if chunk.Done {
					break
				}
				if chunk.NextOffset <= offset {
					t.Fatal("content reader failed to advance")
				}
				offset = chunk.NextOffset
			}
			if full.String() != body {
				t.Fatal("follow lost or changed the oversized body")
			}
		})
	}
}
