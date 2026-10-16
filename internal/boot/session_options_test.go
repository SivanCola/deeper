package boot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/session"
)

func TestBuildRejectsMissingHostSessionServiceBeforeAssembly(t *testing.T) {
	// Reject missing ownership before configuration/provider/plugin assembly
	// can create resources or choose the obsolete path-based runtime.
	result, err := BuildRuntime(t.Context(), Options{})
	if result != nil || err == nil || !strings.Contains(err.Error(), "host-owned session service") {
		t.Fatalf("missing owner = %+v, %v", result, err)
	}
}

func TestBuildRejectsForeignSessionRuntimeBeforeAssembly(t *testing.T) {
	makeService := func() *session.Service {
		service, err := session.NewService("host", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "current")))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
		return service
	}
	owner, foreign := makeService(), makeService()
	runtime, err := owner.Create(t.Context(), session.CreateOptions{SessionID: "same-id"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Create(t.Context(), session.CreateOptions{SessionID: "same-id"}); err != nil {
		t.Fatal(err)
	}
	result, err := BuildRuntime(t.Context(), Options{SessionService: foreign, SessionRuntime: runtime})
	if result != nil || err == nil || !strings.Contains(err.Error(), "exact runtime") {
		t.Fatalf("foreign owner = %+v, %v", result, err)
	}
	if current, ok := owner.Runtime(runtime.Ref()); !ok || current != runtime {
		t.Fatal("rejected assembly retired the real owner")
	}
}
