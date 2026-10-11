package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/tool"
)

func recoveryHTTPFixture(t *testing.T) (*httptest.Server, control.ToolRecoverySnapshot) {
	t.Helper()
	bc := NewBroadcaster()
	a := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, bc)
	c := control.New(control.Options{Executor: a, Sink: bc})
	t.Cleanup(c.Close)
	s := httptest.NewServer(operatorHandler(New(c, bc, config.ServeConfig{})))
	t.Cleanup(s.Close)
	return s, c.ToolRecoverySnapshot()
}

func TestToolRecoveryHTTPRoutesAndFences(t *testing.T) {
	s, v := recoveryHTTPFixture(t)
	response, err := http.Get(s.URL + "/tool-recovery")
	if err != nil {
		t.Fatal(err)
	}
	var got control.ToolRecoverySnapshot
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || got.Revision != v.Revision || got.Calls == nil {
		t.Fatalf("snapshot=%+v status=%d", got, response.StatusCode)
	}
	response, err = http.Post(s.URL+"/tool-recovery", "application/json", strings.NewReader(`{"action":"confirm"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("removed endpoint returned %d", response.StatusCode)
	}
}
