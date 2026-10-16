package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWhileIdleExcludesWritersWithoutMutatingParent(t *testing.T) {
	root := t.TempDir()
	persistence := NewFilesystemPersistence(root)
	service, err := NewService("local", persistence)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "parent", Origin: SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	ref := runtime.Ref()
	query := service.Query()
	called := false
	work := func() error { called = true; return nil }
	if acquired, err := query.WhileIdle(t.Context(), ref.SessionID, work); err != nil || acquired || called {
		t.Fatalf("live writer: acquired=%v called=%v err=%v", acquired, called, err)
	}
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	dir, err := persistence.sessionDir(ref.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	readFiles := func() map[string]string {
		t.Helper()
		files := map[string]string{}
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) == ".lock" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err == nil {
				files[path] = string(data)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := readFiles()
	wantErr := errors.New("maintenance failed")
	acquired, err := query.WhileIdle(t.Context(), ref.SessionID, func() error {
		if _, err := service.Open(t.Context(), ref); !errors.Is(err, ErrWriterOwned) {
			t.Fatalf("concurrent open during maintenance = %v", err)
		}
		return wantErr
	})
	if !acquired || !errors.Is(err, wantErr) {
		t.Fatalf("maintenance = %v, %v", acquired, err)
	}
	if after := readFiles(); !reflect.DeepEqual(before, after) {
		t.Fatal("maintenance changed parent bytes")
	}
	binding, err := service.Open(t.Context(), ref)
	if err != nil {
		t.Fatalf("guard did not release on failure: %v", err)
	}
	if err := binding.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if acquired, err := query.WhileIdle(ctx, ref.SessionID, work); acquired || called || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled maintenance = %v, called=%v err=%v", acquired, called, err)
	}
	if acquired, err := query.WhileIdle(t.Context(), "missing", work); err != nil || acquired || called {
		t.Fatalf("missing parent = %v, called=%v err=%v", acquired, called, err)
	}
}

func TestStartupMaintenanceCannotRunAfterExecutionOrOnAnotherCatalog(t *testing.T) {
	service, err := NewService("local", NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "startup", Origin: SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	query := service.Query()
	called := 0
	work := func() error { called++; return nil }
	before := runtime.Session().Snapshot()
	if acquired, err := query.BeforeFirstExecution(t.Context(), runtime, work); err != nil || !acquired || called != 1 {
		t.Fatalf("startup maintenance: %v, calls=%d, %v", acquired, called, err)
	}
	if after := runtime.Session().Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatal("startup maintenance changed parent")
	}
	other, err := NewService("local", NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Shutdown(context.Background()) })
	if acquired, err := other.Query().BeforeFirstExecution(t.Context(), runtime, work); acquired || err == nil || called != 1 {
		t.Fatalf("foreign catalog granted maintenance: %v, %v", acquired, err)
	}
	execution := &testExecution{phase: RuntimeIdle}
	generation := runtime.BindExecution(execution)
	if generation == 0 {
		t.Fatal("execution binding failed")
	}
	if acquired, err := query.BeforeFirstExecution(t.Context(), runtime, work); err != nil || acquired || called != 1 {
		t.Fatalf("bound runtime granted maintenance: %v, %v", acquired, err)
	}
	runtime.UnbindExecution(generation)
	if acquired, err := query.BeforeFirstExecution(t.Context(), runtime, work); err != nil || acquired || called != 1 {
		t.Fatalf("previously executed runtime granted maintenance: %v, %v", acquired, err)
	}
}
