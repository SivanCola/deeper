package acp

import (
	"context"
	"deepzora/internal/permission"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"errors"
	"testing"
)

func leaseTestFactory(dir string) *e2eFactory {
	return &e2eFactory{prov: &scriptedProvider{name: "fake", responses: [][]provider.Chunk{{{Type: provider.ChunkText, Text: "unused"}, {Type: provider.ChunkDone}}}}, tool: fakeTool{name: "peek", ro: true, out: "unused"}, policy: permission.New("read-only", nil, nil, nil), sessionDir: dir}
}

func TestSessionCloseReleasesCurrentWriter(t *testing.T) {
	dir := t.TempDir()
	client, stop := startServer(t, leaseTestFactory(dir))
	defer stop()
	id := openSession(t, client)
	persistence := session.NewFilesystemPersistence(session.RootForSessionDir(dir))
	held, err := persistence.Open(id, session.ReadWrite)
	if held != nil {
		_ = held.Close(context.Background())
	}
	if !errors.Is(err, session.ErrWriterOwned) {
		t.Fatalf("writer was not held: %v", err)
	}
	response := client.call(t, "session/close", SessionCloseParams{SessionID: id})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	closeTestACPService(dir)
	writer, err := persistence.Open(id, session.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
