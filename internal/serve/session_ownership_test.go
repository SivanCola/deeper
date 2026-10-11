package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/provider"
)

// withForeignWriterLease models the local writer as a separate process. The
// real probe answers false for leases held by the calling process, so tests
// substitute one backed by the writer lease they hold in-process.
func withForeignWriterLease(t *testing.T, session string, held *atomic.Bool) {
	t.Helper()
	prev := leaseHeldByForeignRuntime
	canonical := agent.CanonicalSessionPath(session)
	leaseHeldByForeignRuntime = func(path string) bool {
		return held.Load() && agent.CanonicalSessionPath(path) == canonical
	}
	t.Cleanup(func() { leaseHeldByForeignRuntime = prev })
}

// extendSessionOnDisk appends a message as the local writer would: load the
// transcript (establishing its CAS baseline), add the turn, save.
func extendSessionOnDisk(t *testing.T, path, content string) {
	t.Helper()
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("writer load: %v", err)
	}
	loaded.Add(provider.Message{Role: provider.RoleUser, Content: content})
	if err := loaded.Save(path); err != nil {
		t.Fatalf("writer save: %v", err)
	}
}

// runningForeverController keeps RuntimeStatus busy so drain-mode handoff has
// something to wait on.
type runningForeverController struct {
	*control.Controller
}

func (c *runningForeverController) RuntimeStatus() control.RuntimeStatus {
	return control.RuntimeStatus{Running: true}
}

type snapshotFailController struct {
	*control.Controller
}

func (c *snapshotFailController) Snapshot() error { return fmt.Errorf("snapshot failed") }

type ownershipFixture struct {
	server *Server
	srv    *httptest.Server
	leases *control.SessionLeaseKeeper
	active string
	dir    string
	grant  mirrorGrant
}

func newOwnershipFixture(t *testing.T) *ownershipFixture {
	t.Helper()
	dir := t.TempDir()
	active := filepath.Join(dir, "active.jsonl")
	saveServeTestSession(t, active)

	bc := NewBroadcaster()
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, bc)
	ctrl := control.New(control.Options{Executor: exec, Sink: bc, SessionDir: dir, SessionPath: active})
	server := newLifecycleTestServer(t, ctrl, bc, config.ServeConfig{})
	leases := control.NewSessionLeaseKeeper()
	if err := leases.Rebind(active); err != nil {
		t.Fatalf("seed lease on active: %v", err)
	}
	server.SetSessionLeases(leases)
	t.Cleanup(func() {
		leases.Release()
		ctrl.Close()
	})
	fixture := &ownershipFixture{server: server, leases: leases, active: active, dir: dir}
	fixture.srv = httptest.NewServer(operatorHandler(server))
	t.Cleanup(fixture.srv.Close)
	return fixture
}

func (f *ownershipFixture) post(t *testing.T, path string, body any) (int, string) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(f.srv.URL+path, "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, strings.TrimSpace(buf.String())
}

func (f *ownershipFixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(f.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, strings.TrimSpace(buf.String())
}

func (f *ownershipFixture) ownershipView(t *testing.T, session string) ownershipView {
	t.Helper()
	status, body := f.get(t, "/ownership?session="+filepath.ToSlash(session))
	if status != http.StatusOK {
		t.Fatalf("GET /ownership status = %d (body %q)", status, body)
	}
	var view ownershipView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("decode ownership view: %v (body %q)", err, body)
	}
	return view
}

// handoffForce performs the takeover a confirmed local window would issue.
func (f *ownershipFixture) handoffForce(t *testing.T, mode string) (int, string) {
	t.Helper()
	status, body := f.post(t, "/handoff", map[string]any{
		"sessionPath":    f.active,
		"targetWriterId": agent.SessionWriterID(),
		"force":          true,
		"mode":           mode,
		"timeoutMs":      3000,
	})
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &f.grant); err != nil {
			t.Fatalf("decode handoff grant: %v", err)
		}
	}
	return status, body
}

func (f *ownershipFixture) acquireHandedOff(t *testing.T) *agent.SessionLease {
	t.Helper()
	lease, err := agent.TryAcquireSessionLeaseWithHandoff(f.active, f.grant.SourceWriterID, f.grant.HandoffID)
	if err != nil {
		t.Fatalf("acquire handed-off lease: %v", err)
	}
	return lease
}

func readAllOrFatal(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return strings.TrimSpace(buf.String())
}

func ownershipViewFromServer(t *testing.T, srv *httptest.Server, session string) ownershipView {
	t.Helper()
	resp, err := http.Get(srv.URL + "/ownership?session=" + filepath.ToSlash(session))
	if err != nil {
		t.Fatal(err)
	}
	body := readAllOrFatal(t, resp)
	var view ownershipView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("decode ownership view: %v (body %q)", err, body)
	}
	return view
}

// serveEventStream reads an SSE endpoint frame by frame.
type serveEventStream struct {
	lines chan string
	stop  chan struct{}
}

func subscribeServeEvents(t *testing.T, url string) *serveEventStream {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("subscribe %s status = %d", url, resp.StatusCode)
	}
	stream := &serveEventStream{lines: make(chan string, 64), stop: make(chan struct{})}
	go func() {
		defer close(stream.lines)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			select {
			case <-stream.stop:
				return
			default:
			}
			line := scanner.Text()
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				select {
				case stream.lines <- data:
				case <-stream.stop:
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		select {
		case <-stream.stop:
		default:
			close(stream.stop)
		}
	})
	return stream
}

func (s *serveEventStream) next(out any, timeout time.Duration) error {
	select {
	case line := <-s.lines:
		return json.Unmarshal([]byte(line), out)
	case <-time.After(timeout):
		return fmt.Errorf("timed out waiting for an SSE frame")
	}
}

func (s *serveEventStream) close() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}
