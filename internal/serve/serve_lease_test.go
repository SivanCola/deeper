package serve

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
)

func saveServeTestSession(t *testing.T, path string) {
	t.Helper()
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
}

func readAll(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b)), err
}

func postServeLeaseJSON(client *http.Client, url string, body any, want int) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := client.Post(url, "application/json", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	respBody, readErr := readAll(resp)
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode != want {
		return fmt.Errorf("POST %s status = %d, want %d (body %q)", url, resp.StatusCode, want, respBody)
	}
	return nil
}

func waitServeLeaseDone(t *testing.T, done <-chan struct{}, what string, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitServeLeaseResult(t *testing.T, ch <-chan error, what string, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("timed out waiting for %s", what)
	}
}

const concurrentServeLeaseTimeout = 60 * time.Second
