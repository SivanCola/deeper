package serve

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"deepzora/internal/control"
	"deepzora/internal/session"
)

func TestBindErrorMapsCurrentWriterOwnershipToConflict(t *testing.T) {
	response := httptest.NewRecorder()
	(&Server{}).renderBindError(response, fmt.Errorf("open private-session: %w", session.ErrWriterOwned))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), control.SessionInUseMessage) {
		t.Fatalf("writer conflict response = %d %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-session") {
		t.Fatal("writer conflict leaked private session identity")
	}
}
