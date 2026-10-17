package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCurrentForkTitleUsesHarnessNumbering(t *testing.T) {
	srv, ctrl, service, source := newHistoryCommandServe(t)
	if err := service.SetTitle(t.Context(), source, "Roadmap (1)"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	srv.fork(response, httptest.NewRequest(http.MethodPost, "/fork", strings.NewReader(`{"turn":1}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("fork status = %d: %s", response.Code, response.Body.String())
	}
	child, _ := ctrl.SessionRef()
	info, err := service.Query().Stat(t.Context(), child)
	if err != nil || child == source || info.Title != "Roadmap (2)" {
		t.Fatalf("fork title = %+v, %v", info, err)
	}
}

func TestPreviewTitleStripsOnlyLeadingPasteLabel(t *testing.T) {
	if got := previewTitle("[Pasted text #2 · 42 lines]\nfunc foo() { return 1 }"); got != "func foo() { return 1 }" {
		t.Fatalf("previewTitle = %q", got)
	}
	const inline = "Explain [Pasted text #2 · 42 lines] handling"
	if got := previewTitle(inline); got != inline {
		t.Fatalf("inline label changed to %q", got)
	}
}
