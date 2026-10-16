package session

import (
	"context"
	"testing"
)

func TestSubagentCatalogOwnership(t *testing.T) {
	parent, err := NewService("host", NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Shutdown(context.Background()) })
	children, err := parent.Subagents()
	if err != nil {
		t.Fatal(err)
	}
	again, err := parent.Subagents()
	if err != nil || again != children {
		t.Fatalf("replacement catalog = %p, %v", again, err)
	}
	runtime, err := children.Create(t.Context(), CreateOptions{SessionID: "child"})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := parent.Query().List(t.Context(), "", 100)
	if err != nil || len(listed.Sessions) != 0 {
		t.Fatalf("parent list includes child: %+v, %v", listed, err)
	}
	if err := parent.CloseAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := children.Runtime(runtime.Ref()); !ok {
		t.Fatal("parent CloseAll closed child writer")
	}
	if err := parent.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := children.Runtime(runtime.Ref()); ok {
		t.Fatal("parent shutdown left child writer")
	}
	if _, err := parent.Subagents(); err == nil {
		t.Fatal("shutdown parent allowed a new child catalog")
	}
	// Closing the parent released the filesystem writer, including children.
	other, err := NewService("host", NewFilesystemPersistence(children.Query().StorageRoot()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Shutdown(context.Background()) })
	binding, err := other.Open(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
}
