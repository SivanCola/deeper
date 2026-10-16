package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/subagentpersist"
)

type benchmarkSession struct {
	service      *session.Service
	handle       agent.SubagentTranscript
	conversation *agent.Session
}

func openBenchmarkSession(dir string, seed []provider.Message) *benchmarkSession {
	service, err := session.NewService("benchmark", session.NewFilesystemPersistence(filepath.Join(dir, "sessions")))
	if err != nil {
		failPersistence(err)
	}
	adapter := subagentpersist.New(service)
	var handle agent.SubagentTranscript
	if seed != nil {
		for i := range seed {
			seed[i].ID = agent.NewMessageID()
		}
		handle, err = adapter.Create(context.Background(), agent.SubagentMeta{Ref: "benchmark"}, seed)
	} else {
		handle, err = adapter.Open(context.Background(), "benchmark")
	}
	if err != nil {
		_ = service.Shutdown(context.Background())
		failPersistence(err)
	}
	conversation := agent.NewSession("")
	conversation.Replace(handle.Messages())
	return &benchmarkSession{service: service, handle: handle, conversation: conversation}
}

func (s *benchmarkSession) append(messages []provider.Message) {
	for i := range messages {
		messages[i].ID = agent.NewMessageID()
	}
	if err := s.handle.RecordSessionMessages(context.Background(), "benchmark-growth", messages); err != nil {
		failPersistence(err)
	}
	s.conversation.AddBatch(messages...)
}

func (s *benchmarkSession) flush() {
	if err := s.handle.Flush(context.Background()); err != nil {
		failPersistence(err)
	}
}

func (s *benchmarkSession) close() {
	if err := errors.Join(s.handle.Close(context.Background()), s.service.Shutdown(context.Background())); err != nil {
		failPersistence(err)
	}
}

func failPersistence(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
