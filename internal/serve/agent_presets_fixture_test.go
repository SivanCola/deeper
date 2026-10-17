package serve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/ablation"
	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/eventwire"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/stats"
)

var presetHTTPProviderSequence atomic.Uint64

type presetHTTPProvider struct {
	requests chan provider.Request
	hold     chan struct{}
}

func (p *presetHTTPProvider) Name() string { return "serve-preset-fixture" }
func (p *presetHTTPProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.requests <- req
	ch := make(chan provider.Chunk, 2)
	if p.hold == nil {
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
		ch <- provider.Chunk{Type: provider.ChunkDone}
		close(ch)
		return ch, nil
	}
	go func() {
		defer close(ch)
		select {
		case <-p.hold:
			ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
			ch <- provider.Chunk{Type: provider.ChunkDone}
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

type presetHTTPFixture struct {
	server   *Server
	http     *httptest.Server
	provider *presetHTTPProvider
	service  *session.Service
}

func newPresetHTTPFixture(t *testing.T, hold bool) *presetHTTPFixture {
	t.Helper()
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	t.Cleanup(func() {
		if err := stats.CloseRecordDispatchers(context.Background()); err != nil {
			t.Errorf("close usage dispatchers: %v", err)
		}
		if err := stats.CloseUsageCatalogs(context.Background()); err != nil {
			t.Errorf("close usage catalogs: %v", err)
		}
	})
	root, sessionDir := t.TempDir(), t.TempDir()
	service, err := session.NewService("preset-http", session.NewFilesystemPersistence(filepath.Join(sessionDir, "sessions-v4")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	prov := &presetHTTPProvider{requests: make(chan provider.Request, 16)}
	if hold {
		prov.hold = make(chan struct{})
	}
	kind := "serve-agent-preset-http-fixture-" + strconv.FormatUint(presetHTTPProviderSequence.Add(1), 10)
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return prov, nil })
	if err := os.WriteFile(config.UserConfigPath(), []byte("default_model = \"preset/model\"\n[[providers]]\nname = \"preset\"\nkind = \""+kind+"\"\nmodels = [\"model\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bc := NewBroadcaster()
	tag := NewSessionTagSink(bc)
	opts := boot.Options{
		Model: "preset/model", WorkspaceRoot: root, SessionDir: sessionDir,
		SessionService: service, SessionHostID: "preset-http", Sink: tag, Stderr: io.Discard,
		Ablation: ablation.New(ablation.Modules()...),
	}
	ctrl, err := boot.Build(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.BindFreshSession(t.Context(), "initial"); err != nil {
		ctrl.Close()
		t.Fatal(err)
	}
	tag.SetIdentity("", "initial")
	srv := New(ctrl, bc, config.ServeConfig{AuthMode: "none", Token: "preset-token"})
	srv.SetControllerBuildOptions(opts)
	srv.RegisterSessionTag(ctrl, tag)
	t.Cleanup(srv.Close)
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return &presetHTTPFixture{server: srv, http: httpSrv, provider: prov, service: service}
}

func (f *presetHTTPFixture) request(t *testing.T, method, path string, body any, status int) []byte {
	t.Helper()
	var text string
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		text = string(encoded)
	}
	req, err := http.NewRequest(method, f.http.URL+path, strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer preset-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, status, data)
	}
	return data
}

func (f *presetHTTPFixture) save(t *testing.T, scope, id, content string) {
	t.Helper()
	f.request(t, http.MethodPut, "/agent-presets/"+id, map[string]string{"scope": scope, "content": content}, http.StatusOK)
}

func (f *presetHTTPFixture) list(t *testing.T) control.AgentPresetList {
	t.Helper()
	var list control.AgentPresetList
	if err := json.Unmarshal(f.request(t, http.MethodGet, "/agent-presets", nil, http.StatusOK), &list); err != nil {
		t.Fatal(err)
	}
	return list
}

func (f *presetHTTPFixture) run(t *testing.T) provider.Request {
	t.Helper()
	frames, stop := f.server.bc.SubscribeAll()
	defer stop()
	f.request(t, http.MethodPost, "/submit", map[string]string{"input": "reply briefly"}, http.StatusAccepted)
	var req provider.Request
	select {
	case req = <-f.provider.requests:
	case <-time.After(30 * time.Second):
		t.Fatal("fixture provider received no request")
	}
	for {
		select {
		case data := <-frames:
			var frame eventwire.Event
			if err := json.Unmarshal(data, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Kind == "turn_done" {
				return req
			}
		case <-time.After(30 * time.Second):
			t.Fatal("fixture turn never completed")
		}
	}
}

func assertPresetProviderContract(t *testing.T, req provider.Request, body string, noTools bool) {
	t.Helper()
	if len(req.Messages) == 0 || !strings.Contains(req.Messages[0].Content, body) {
		t.Fatalf("provider system prompt does not contain %q", body)
	}
	if noTools {
		for _, tool := range req.Tools {
			if tool.Name != "use_capability" {
				t.Fatalf("preset with tools: [] exposed tool %q", tool.Name)
			}
		}
	}
}
