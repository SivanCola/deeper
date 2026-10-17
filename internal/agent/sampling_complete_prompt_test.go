package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/extension"
	"deepzora/internal/extension/dispatch"
	"deepzora/internal/extension/protocol"
	"deepzora/internal/extension/providerconv"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func TestCompleteSystemPromptSurvivesExtensionSystems(t *testing.T) {
	const body = "  Complete preset body.\nKeep these exact bytes.\n"
	for _, intercepts := range []struct {
		name    string
		context bool
		request bool
	}{
		{"context.prepare", true, false},
		{"provider.request", false, true},
		{"both", true, true},
	} {
		for _, shape := range []string{"override", "no systems", "multiple systems"} {
			for _, complete := range []string{body, ""} {
				mode := "complete"
				if complete == "" {
					mode = "default"
				}
				t.Run(intercepts.name+"/"+shape+"/"+mode, func(t *testing.T) {
					var expected provider.Request
					client := &fakeDispatchClient{interceptFn: func(ev protocol.InterceptEvent, payload json.RawMessage) (protocol.InterceptResult, error) {
						switch ev {
						case protocol.EventContextPrepare:
							if !intercepts.context {
								return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
							}
							var in dispatch.ContextPayload
							if err := json.Unmarshal(payload, &in); err != nil {
								return protocol.InterceptResult{}, err
							}
							in.Messages = completePromptExtensionSystems(in.Messages, shape, string(ev))
							return replaceWith(t, in), nil
						case protocol.EventProviderRequest:
							var in dispatch.ProviderRequestPayload
							if err := json.Unmarshal(payload, &in); err != nil {
								return protocol.InterceptResult{}, err
							}
							if intercepts.request {
								in.Request.Messages = completePromptExtensionSystems(in.Request.Messages, shape, string(ev))
							}
							// Non-system extension rulings must survive the complete-body guard.
							in.Request.MaxTokens = 2345
							in.Request.Temperature = provider.OptionalTemperature(0.375)
							in.Request.ResponseFormat = &protocol.ProviderResponseFormat{Type: "json_object"}
							expected = providerconv.RequestFromProtocol(in.Request)
							return replaceWith(t, in), nil
						}
						return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
					}}
					d := newExtDispatcher(client, true, nil, extension.PointContextPrepare, extension.PointProviderRequest)
					mp := &mockProvider{name: "p", chunks: []provider.Chunk{{Type: provider.ChunkDone}}}
					reg := tool.NewRegistry()
					reg.Add(&recordingTool{name: "read_file", readOnly: true})
					reg.Add(&recordingTool{name: "grep", readOnly: true})
					sess := NewSession("durable system")
					nonSystem := []provider.Message{
						{Role: provider.RoleUser, Content: "user payload\n  exact whitespace"},
						{Role: provider.RoleAssistant, Content: "reading", ReasoningContent: "read the file", ToolCalls: []provider.ToolCall{{ID: "a", Name: "read_file", Arguments: ` { "path": "file" } `}}},
						{Role: provider.RoleTool, Name: "read_file", ToolCallID: "a", Content: "file contents"},
						{Role: provider.RoleUser, Content: "next payload", Images: []string{"https://example.com/image.png"}},
					}
					for _, message := range nonSystem {
						sess.Add(message)
					}
					before := sess.Snapshot()
					state := newCurrentPersistenceSession(t, sess)
					persistedBefore := state.history(t).Snapshot()
					a := New(mp, reg, sess, Options{CompleteSystemPrompt: complete, Extensions: d}, event.Discard)
					prepared, err := a.prepareSamplingRequest(context.Background())
					if err != nil {
						t.Fatalf("prepareSamplingRequest: %v", err)
					}
					ch, err := a.streamProviderRequest(context.Background(), prepared.req)
					if err != nil {
						t.Fatalf("streamProviderRequest: %v", err)
					}
					for range ch {
					}
					if len(mp.requests) != 1 {
						t.Fatalf("provider requests = %d, want 1", len(mp.requests))
					}
					if complete != "" {
						expected.Messages = append([]provider.Message{{Role: provider.RoleSystem, Content: body}}, nonSystem...)
					}
					if got := mp.requests[0]; !reflect.DeepEqual(got, expected) {
						t.Fatalf("provider request changed beyond system guard\ngot: %+v\nwant: %+v", got, expected)
					}
					if got := sess.Snapshot(); !reflect.DeepEqual(got, before) {
						t.Fatalf("sampling changed durable history\ngot: %+v\nwant: %+v", got, before)
					}
					state.reopen(t, state.ref)
					if persistedAfter := state.history(t).Snapshot(); !reflect.DeepEqual(persistedAfter, persistedBefore) {
						t.Fatal("sampling changed persisted history")
					}
				})
			}
		}
	}
}

func TestCompleteSystemPromptWithoutExtensions(t *testing.T) {
	for _, shape := range []string{"no systems", "multiple systems"} {
		for _, body := range []string{"complete body", " \n\t"} {
			t.Run(shape+"/"+body, func(t *testing.T) {
				mp := &mockProvider{name: "p", chunks: []provider.Chunk{{Type: provider.ChunkDone}}}
				sess := NewSession("")
				if shape == "multiple systems" {
					sess.Add(provider.Message{Role: provider.RoleSystem, Content: "old leading", Name: "old"})
				}
				sess.Add(provider.Message{Role: provider.RoleUser, Content: "user payload", CreatedAt: 1234})
				if shape == "multiple systems" {
					sess.Add(provider.Message{Role: provider.RoleSystem, Content: "old trailing"})
				}
				before := sess.Snapshot()
				a := New(mp, tool.NewRegistry(), sess, Options{CompleteSystemPrompt: body}, event.Discard)
				prepared, err := a.prepareSamplingRequest(context.Background())
				if err != nil {
					t.Fatalf("prepareSamplingRequest: %v", err)
				}
				ch, err := a.streamProviderRequest(context.Background(), prepared.req)
				if err != nil {
					t.Fatalf("streamProviderRequest: %v", err)
				}
				for range ch {
				}
				want := []provider.Message{{Role: provider.RoleSystem, Content: body}}
				for _, message := range before {
					if message.Role != provider.RoleSystem {
						message.CreatedAt = 0
						want = append(want, message)
					}
				}
				if len(mp.requests) != 1 || !reflect.DeepEqual(mp.requests[0].Messages, want) {
					t.Fatalf("provider requests = %+v, want messages %+v", mp.requests, want)
				}
				if got := sess.Snapshot(); !reflect.DeepEqual(got, before) {
					t.Fatal("complete prompt guard mutated durable history")
				}
				// The request's message structs must remain detached from history.
				mp.requests[0].Messages[1].Content = "provider changed its request copy"
				if got := sess.Snapshot(); !reflect.DeepEqual(got, before) {
					t.Fatal("provider request message slice aliases durable history")
				}
			})
		}
	}
}

func completePromptExtensionSystems(messages []protocol.ProviderMessage, shape, source string) []protocol.ProviderMessage {
	var systems []protocol.ProviderMessage
	switch shape {
	case "override":
		systems = []protocol.ProviderMessage{{Role: protocol.ProviderRoleSystem, Content: source + " override", Name: "extension"}}
	case "multiple systems":
		systems = []protocol.ProviderMessage{{Role: protocol.ProviderRoleSystem, Content: source + " leading"}}
	}
	for _, message := range messages {
		if message.Role != protocol.ProviderRoleSystem {
			systems = append(systems, message)
		}
	}
	if shape == "multiple systems" {
		systems = append(systems, protocol.ProviderMessage{Role: protocol.ProviderRoleSystem, Content: source + " trailing"})
	}
	return systems
}
