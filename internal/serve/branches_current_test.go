package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"deepzora/internal/control"
	"deepzora/internal/session"
)

func TestBranchesHTTPReadsCurrentScopedCatalog(t *testing.T) {
	allowed := map[string]bool{"saved": true}
	srv, ctrl, service, before := newExclusiveSessionServeWithOptions(t, func(opts *control.Options) {
		opts.SessionReadScope = func(context.Context) (map[string]bool, error) { return allowed, nil }
	})
	defer srv.Close()
	var saved session.SessionRef
	for _, id := range []string{"saved", "foreign"} {
		runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.SetTitle(t.Context(), runtime.Ref(), "Title "+id); err != nil {
			t.Fatal(err)
		}
		if err := service.Close(t.Context(), runtime.Ref()); err != nil {
			t.Fatal(err)
		}
		if id == "saved" {
			saved = runtime.Ref()
		}
	}
	read := func() ([]map[string]any, string) {
		t.Helper()
		response := httptest.NewRecorder()
		operatorHandler(srv).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/branches", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("branches HTTP %d: %s", response.Code, response.Body.String())
		}
		var result struct {
			Branches []map[string]any `json:"branches"`
			Tree     string           `json:"tree"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Branches, result.Tree
	}
	rows, tree := read()
	if len(rows) != 1 || rows[0]["id"] != "saved" || rows[0]["name"] != "Title saved" || !strings.Contains(tree, "Title saved") {
		t.Fatalf("current branch payload = %+v, tree %q", rows, tree)
	}
	for _, field := range []string{"preview", "turns", "model", "parent_id", "created_at", "updated_at"} {
		if _, ok := rows[0][field]; !ok {
			t.Fatalf("current branch payload is missing %s", field)
		}
	}
	for _, field := range []string{"Path", "HeadID", "CustomTitle", "custom_title", "LogSchema"} {
		if _, ok := rows[0][field]; ok {
			t.Fatalf("branch payload still exposes sidecar field %s", field)
		}
	}
	if _, live := service.Runtime(saved); live {
		t.Fatal("branch HTTP read acquired a saved session writer")
	}
	if current, _ := ctrl.SessionRef(); current != before {
		t.Fatal("branch HTTP read changed the active identity")
	}
	delete(allowed, "saved")
	rows, tree = read()
	if len(rows) != 0 || tree != "branches: none" {
		t.Fatalf("revoked branch still visible: %+v, %q", rows, tree)
	}
}
