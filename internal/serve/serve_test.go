package serve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/permission"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

// fakeRunner stands in for an agent.Runner: it records the composed input and
// returns without emitting model events, so the controller's TurnDone is the
// observable signal.
type fakeRunner struct{ got chan string }

func (f fakeRunner) Run(_ context.Context, input string) error { f.got <- input; return nil }

type serveApprovalWriter struct{}

func (serveApprovalWriter) Name() string        { return "serve_write" }
func (serveApprovalWriter) Description() string { return "write a test file" }
func (serveApprovalWriter) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}
func (serveApprovalWriter) ReadOnly() bool { return false }
func (serveApprovalWriter) Execute(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}

type serveApprovalProvider struct {
	mu   sync.Mutex
	turn int
}

func (p *serveApprovalProvider) Name() string { return "serve-approval-test" }
func (p *serveApprovalProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	turn := p.turn
	p.turn++
	p.mu.Unlock()

	ch := make(chan provider.Chunk, 2)
	if turn == 0 {
		ch <- provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{
			ID: "serve-approval-1", Name: "serve_write", Arguments: `{"path":"a.txt"}`,
		}}
	} else {
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "done"}
	}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func TestServeSubmitRunsAndBroadcastsTurnDone(t *testing.T) {
	bc := NewBroadcaster()
	got := make(chan string, 1)
	ctrl := control.New(control.Options{Runner: fakeRunner{got: got}, Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	sub, cancel := bc.Subscribe() // observe the broadcast deterministically
	defer cancel()

	resp, err := http.Post(srv.URL+"/submit", "application/json", strings.NewReader(`{"input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status = %d, want 202", resp.StatusCode)
	}

	select {
	case in := <-got:
		if in != "hi" {
			t.Errorf("runner ran %q, want hi", in)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner never ran")
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case data := <-sub:
			var w eventwire.Event
			if err := json.Unmarshal(data, &w); err == nil && w.Kind == "turn_done" {
				return
			}
		case <-deadline:
			t.Fatal("never saw turn_done on the stream")
		}
	}
}

func TestServeEndpoints(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc}) // no runner needed for these
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	if resp, err := http.Get(srv.URL + "/history"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("history = %v / %v", resp, err)
	}

	if resp, _ := http.Get(srv.URL + "/context"); resp.StatusCode != http.StatusOK {
		t.Errorf("context status = %d", resp.StatusCode)
	}

	resp, err := http.Post(srv.URL+"/plan", "application/json", strings.NewReader(`{"on":true}`))
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("plan = %v / status %d", err, resp.StatusCode)
	}
	if c := ctrl.Compose("x"); !strings.Contains(c, "Plan mode") {
		t.Error("/plan {on:true} should have enabled plan mode (Compose would prepend the marker)")
	}

	resp, err = http.Post(srv.URL+"/tool-approval-mode", "application/json", strings.NewReader(`{"mode":"workspace-write"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("tool approval mode auto status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()
	if got := ctrl.ToolApprovalMode(); got != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("tool approval mode = %q, want auto", got)
	}
	resp, err = http.Post(srv.URL+"/tool-approval-mode", "application/json", strings.NewReader(`{"mode":"surprise"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid tool approval mode status = %d, want 400", resp.StatusCode)
	}

	if resp, _ := http.Post(srv.URL+"/submit", "application/json", strings.NewReader(`{}`)); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty submit should be 400, got %d", resp.StatusCode)
	}
}

func TestServeSubmitRejectsShellShortcut(t *testing.T) {
	bc := NewBroadcaster()
	got := make(chan string, 1)
	ctrl := control.New(control.Options{Runner: fakeRunner{got: got}, Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/submit", "application/json", strings.NewReader(`{"input":"!echo nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("shell submit status = %d, want 403", resp.StatusCode)
	}
	select {
	case in := <-got:
		t.Fatalf("runner should not run shell submit, got %q", in)
	default:
	}
}

func TestServeSubmitValidatesFormat(t *testing.T) {
	bc := NewBroadcaster()
	got := make(chan string, 1)
	ctrl := control.New(control.Options{Runner: fakeRunner{got: got}, Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	post := func(body string) int {
		resp, err := http.Post(srv.URL+"/submit", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// Unsupported format is rejected with 400 and the runner never runs.
	if code := post(`{"input":"hi","format":"xml"}`); code != http.StatusBadRequest {
		t.Fatalf("unsupported format status = %d, want 400", code)
	}
	select {
	case in := <-got:
		t.Fatalf("runner must not run for rejected format, got %q", in)
	default:
	}

	// Whitespace-padded json_object is normalized and accepted.
	if code := post(`{"input":"hi","format":"  json_object  "}`); code != http.StatusAccepted {
		t.Fatalf("padded json_object status = %d, want 202", code)
	}
	select {
	case in := <-got:
		if in != "hi" {
			t.Fatalf("runner ran %q, want hi", in)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner never ran for padded json_object")
	}
}

func TestHistoryMessagesPreserveToolDetails(t *testing.T) {
	got := historyMessages([]provider.Message{
		{Role: provider.RoleUser, Content: "run command"},
		{Role: provider.RoleAssistant, Content: "checking", ReasoningContent: "think", ToolCalls: []provider.ToolCall{{
			ID: "call_1", Name: "bash", Arguments: `{"command":"pwd"}`,
		}}},
		{Role: provider.RoleTool, Name: "bash", ToolCallID: "call_1", Content: "/tmp/project\n"},
	})

	if len(got) != 3 {
		t.Fatalf("history length = %d, want 3", len(got))
	}
	if got[1].Reasoning != "think" {
		t.Fatalf("assistant reasoning = %q, want think", got[1].Reasoning)
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "call_1" || got[1].ToolCalls[0].Name != "bash" || got[1].ToolCalls[0].Arguments != `{"command":"pwd"}` {
		t.Fatalf("assistant tool calls not preserved: %+v", got[1].ToolCalls)
	}
	if got[2].ToolCallID != "call_1" || got[2].ToolName != "bash" || got[2].Content != "/tmp/project\n" {
		t.Fatalf("tool result details not preserved: %+v", got[2])
	}
}

func TestServeCancelEndpoint(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("cancel status = %d, want 204", resp.StatusCode)
	}
}

func TestServeCancelSessionReturnsIdempotentReceipt(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/cancel-session", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var receipt control.CancelReceipt
	if err := json.NewDecoder(resp.Body).Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusAccepted || !receipt.Accepted || !receipt.AlreadyIdle {
		t.Fatalf("cancel receipt status=%d receipt=%+v", resp.StatusCode, receipt)
	}
}

func TestServeApproveMissingID(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	// Missing id should return 400.
	resp, err := http.Post(srv.URL+"/approve", "application/json", strings.NewReader(`{"allow":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("approve missing id = %d, want 400", resp.StatusCode)
	}

	// Malformed JSON should return 400.
	resp2, _ := http.Post(srv.URL+"/approve", "application/json", strings.NewReader(`{bad`))
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("approve bad json = %d, want 400", resp2.StatusCode)
	}

	// Permanent approval was removed from the protocol. Reject it before trying
	// to resolve an ID so legacy clients cannot accidentally persist a grant.
	resp3, err := http.Post(srv.URL+"/approve", "application/json", strings.NewReader(`{"id":"legacy","allow":true,"persist":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Errorf("approve persistent grant = %d, want 400", resp3.StatusCode)
	}
}

func TestServeCompactEndpoint(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/compact", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("compact = %d, want 204", resp.StatusCode)
	}
}

func TestServeIndexDefinesQueryHelpers(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"const $ = s => document.querySelector(s);",
		"const $$ = s => document.querySelectorAll(s);",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing query helper %q", want)
		}
	}
}

func TestServeIndexReportsSessionDeleteFailures(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"'cannot_delete_active': 'Cannot delete the active session'",
		"'cannot_delete_active': '无法删除当前会话'",
		"'delete_failed': 'Could not delete the session. Check your connection and try again.'",
		"'delete_failed': '无法删除会话，请检查连接后重试'",
		"if(target&&target.current){showNotice(__('cannot_delete_active'),'warn');return;}",
		"if(!r.ok){showNotice((await r.text()).trim()||('HTTP '+r.status),'warn');}",
		"}).catch(()=>showNotice(__('delete_failed'),'warn'));",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing session delete failure handling %q", want)
		}
	}
}

func TestServeIndexHandlesRetryingEvents(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"case 'retrying': setRetrying(e.retryAttempt,e.retryMax,e.recovery); break;",
		"if(e.kind!=='retrying')clearRetrying();",
		"'retrying_status': 'Retrying ({attempt}/{max})...'",
		"'retrying_status': '正在重试 ({attempt}/{max})...'",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing retrying support %q", want)
		}
	}
}

func TestServeIndexPresentsRecoveryPauseAsNotice(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"e.outcome==='recovery_paused'",
		"showNotice('⏸ '+__('recovery_paused'))",
		"'recovery_paused': 'Automatic retries paused. Deepzora stopped repeated attempts and kept completed work. Send “Continue” to start a fresh attempt, or add instructions to change direction.'",
		"'recovery_paused': '已暂停自动重试。Deepzora 已停止重复尝试，并保留已完成的工作。发送“继续”即可开始新一轮，也可以补充要求来调整方向。'",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing recovery pause support %q", want)
		}
	}
}

func TestServeIndexRendersAndReloadsExtensions(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"case 'extension_surface': if(e.extension)renderExtensionSurface(e.extension); break;",
		"case 'extension_status': if(e.extension)renderExtensionSurface(e.extension); break;",
		"const node=el('div','notice'",
		"post('/extensions/reload',{})",
		"{cmd:'reload',sig:'/reload'",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing extension support %q", want)
		}
	}
	if strings.Contains(html, "p.card.markdown+'</") {
		t.Fatal("extension Markdown must not be inserted as HTML")
	}
}

func TestServeIndexPagePassesLanguagePreferenceToClient(t *testing.T) {
	closeServeTestUsage(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Cleanup(func() { closeServeTestUsage(t) })

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	defer func() { ctrl.Close(); <-ctrl.Closed() }()
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if !strings.Contains(html, "const __LANG_PREF = 'auto';") {
		t.Fatalf("default language preference was not passed as auto:\n%s", html)
	}
	if !strings.Contains(html, "applyStaticI18n();") {
		t.Fatal("index should translate static __('key') placeholders on the client")
	}

	cfgPath := config.UserConfigPath()
	if cfgPath == "" {
		t.Fatal("user config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("[desktop]\nlanguage = \"en\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "const __LANG_PREF = 'en';") {
		t.Fatalf("pinned desktop language was not passed through:\n%s", string(body))
	}
}

func TestServeModelsMarksActiveByModelRef(t *testing.T) {
	writeServeModelConfig(t)

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{
		Sink:     bc,
		Label:    "shared-chat",
		ModelRef: "alternate/shared-chat",
	})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("models status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Current string `json:"current"`
		Models  []struct {
			Ref    string `json:"ref"`
			Active bool   `json:"active"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	if body.Current != "alternate/shared-chat" {
		t.Fatalf("current = %q, want alternate/shared-chat", body.Current)
	}
	active := map[string]bool{}
	for _, m := range body.Models {
		active[m.Ref] = m.Active
	}
	if active["default/shared-chat"] {
		t.Fatal("default provider was marked active even though the controller is on alternate/shared-chat")
	}
	if !active["alternate/shared-chat"] {
		t.Fatal("alternate/shared-chat was not marked active")
	}
}

func TestServeModelsIncludesExtensionProviderCatalog(t *testing.T) {
	writeServeModelConfig(t)

	bc := NewBroadcaster()
	ref := "plugin/demo/cloud/extension-chat"
	ctrl := control.New(control.Options{
		Sink:     bc,
		Label:    "extension-chat",
		ModelRef: ref,
		ProviderResolver: &provider.StaticResolver{Descriptors: []provider.Descriptor{{
			Ref: ref, Model: "extension-chat", DisplayName: "Extension Chat",
		}},
		},
	})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Models []struct {
			Ref      string `json:"ref"`
			Provider string `json:"provider"`
			Kind     string `json:"kind"`
			Active   bool   `json:"active"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, model := range body.Models {
		if model.Ref == ref {
			if model.Provider != "plugin/demo/cloud" || model.Kind != "extension" || !model.Active {
				t.Fatalf("extension model = %+v", model)
			}
			return
		}
	}
	t.Fatalf("extension provider %q missing from models: %+v", ref, body.Models)
}

func TestServeExtensionReloadPublishesOnlySuccessfulReplacement(t *testing.T) {
	s, old, _, _ := newExclusiveSessionServeWithOptions(t, func(options *control.Options) { options.ModelRef = "default/model" })
	defer s.Close()

	wantErr := errors.New("sidecar did not initialize")
	s.rebuildControllerWithOptions = func(context.Context, *control.Controller, string, boot.Options) (*control.Controller, error) {
		return nil, wantErr
	}
	if err := s.reloadExtensions(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("reload error = %v, want %v", err, wantErr)
	}
	if s.ctl() != old {
		t.Fatal("failed reload replaced the working controller")
	}

	var replacement *control.Controller
	s.rebuildControllerWithOptions = func(_ context.Context, gotOld *control.Controller, ref string, opts boot.Options) (*control.Controller, error) {
		if gotOld != old || ref != "default/model" {
			t.Fatalf("rebuild inputs old=%p ref=%q", gotOld, ref)
		}
		replacement = serveModelReplacement(opts, nil)
		return replacement, nil
	}
	if err := s.reloadExtensions(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if s.ctl() != replacement {
		t.Fatal("successful reload did not publish the replacement")
	}
}

func writeServeModelConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	cfgPath := config.UserConfigPath()
	if cfgPath == "" {
		t.Fatal("user config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `default_model = "default/shared-chat"

[[providers]]
name = "default"
kind = "openai"
base_url = "http://127.0.0.1:1/v1"
models = ["shared-chat"]
default = "shared-chat"
supported_efforts = ["low", "high"]

[[providers]]
name = "alternate"
kind = "openai"
base_url = "http://127.0.0.1:2/v1"
models = ["shared-chat"]
default = "shared-chat"
supported_efforts = ["low", "high"]
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRejectsOldPathWithoutChangingCurrentSession(t *testing.T) {
	server, ctrl, _, current := newExclusiveSessionServe(t)
	pending := filepath.Join(t.TempDir(), "pending.jsonl")
	if err := os.WriteFile(pending, []byte(`{"role":"user","content":"hi"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := strings.TrimSuffix(pending, ".jsonl") + ".cleanup-pending.json"
	if err := os.WriteFile(marker, []byte(`{"operation":"delete"}`), 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(operatorHandler(server))
	defer srv.Close()
	body, err := json.Marshal(map[string]string{"path": pending})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/resume", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cleanup-pending resume status = %d, want 400", resp.StatusCode)
	}
	if got, bound := ctrl.SessionRef(); !bound || got != current {
		t.Fatalf("rejected path changed current identity: %+v", got)
	}
}

func TestServeSubmitMalformedJSON(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/submit", "application/json", strings.NewReader(`{not json`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed submit = %d, want 400", resp.StatusCode)
	}
}

func TestServePlanMalformedJSON(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/plan", "application/json", strings.NewReader(`{bad`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed plan = %d, want 400", resp.StatusCode)
	}
}

func TestServeContextEndpoint(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/context")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("context status = %d", resp.StatusCode)
	}
	var body map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	// Before any turn, used should be 0.
	if body["used"] != 0 {
		t.Errorf("used = %d, want 0", body["used"])
	}
}

// TestServeEventsReplaysPendingAskOnAttach proves a late /events subscriber
// receives a still-blocked ask_request. Without replay, the browser attaches to
// a healthy-looking session that never surfaces the parked prompt (#7643).
func TestServeEventsReplaysPendingAskOnAttach(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	ctrl.EnableInteractiveApproval()
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	firstSub, cancelFirst := bc.Subscribe()
	defer cancelFirst()

	askCtx, cancelAsk := context.WithCancel(context.Background())
	askDone := make(chan error, 1)
	go func() {
		_, err := ctrl.Ask(askCtx, []event.AskQuestion{{
			ID: "q1", Prompt: "pick one", Options: []event.AskOption{{Label: "A"}, {Label: "B"}},
		}})
		askDone <- err
	}()

	if frame := nextServeProtocolFrame(t, firstSub, nil); frame.Kind != "ask_request" {
		t.Fatalf("initial subscriber got %+v, want ask_request", frame)
	}

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/events status = %d", resp.StatusCode)
	}

	replayed := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 512)
		for {
			n, readErr := resp.Body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				if strings.Contains(string(buf), `"kind":"ask_request"`) {
					replayed <- string(buf)
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	select {
	case <-replayed:
	case <-time.After(2 * time.Second):
		t.Fatal("late SSE attach never received replayed ask_request")
	}

	select {
	case err := <-askDone:
		t.Fatalf("ask resolved before the late client answered: %v", err)
	default:
	}

	// Reconnect recovery must be connection-local: the existing subscriber
	// must not receive the same prompt a second time.
	assertNoServeProtocolFrames(t, firstSub)

	cancelAsk()
	select {
	case <-askDone:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked ask did not exit after test cancellation")
	}
}

// TestServeEventsReplayHandoffSerializesPromptEmission proves the controller's
// attach handoff can register a subscriber and replay while prompt emission is
// serialized, so a prompt cannot land between those two operations.
func TestServeEventsReplayHandoffSerializesPromptEmission(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	ctrl.EnableInteractiveApproval()

	askCtx, cancelAsk := context.WithCancel(context.Background())
	defer cancelAsk()
	taskDone := make(chan struct{})
	var sub <-chan []byte
	var cancelSub func()
	ctrl.ReplayPendingPromptsWith(func() event.Sink {
		sub, cancelSub = bc.Subscribe()
		go func() {
			_, _ = ctrl.Ask(askCtx, []event.AskQuestion{{
				ID: "q1", Prompt: "pick one", Options: []event.AskOption{{Label: "A"}, {Label: "B"}},
			}})
			close(taskDone)
		}()
		return event.FuncSink(func(e event.Event) { bc.EmitTo(sub, e) })
	})
	defer cancelSub()

	if frame := nextServeProtocolFrame(t, sub, nil); frame.Kind != "ask_request" {
		t.Fatalf("handoff subscriber got %+v, want ask_request", frame)
	}
	assertNoServeProtocolFrames(t, sub)

	cancelAsk()
	select {
	case <-taskDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handoff ask did not exit after cancellation")
	}
}

// TestServeEventsReplaysPendingApprovalOnAttach covers the actual approval
// surface from #7643: a late browser must receive a parked ApprovalRequest and
// be able to answer it through the serve HTTP endpoint.
func TestServeEventsReplaysPendingApprovalOnAttach(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(serveApprovalWriter{})
	ag := agent.New(&serveApprovalProvider{}, reg, agent.NewSession(""), agent.Options{}, event.Discard)
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{
		Runner:   ag,
		Executor: ag,
		Sink:     bc,
		Policy:   permission.New("ask", nil, []string{"serve_write"}, nil),
	})
	ctrl.SetToolApprovalMode(control.ToolApprovalWorkspaceWrite)
	ctrl.EnableInteractiveApproval()
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	runDone := make(chan error, 1)
	go func() { runDone <- ctrl.Executor().Run(context.Background(), "write a file") }()

	deadline := time.After(2 * time.Second)
	for !ctrl.PendingPrompt() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for parked approval")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/events status = %d", resp.StatusCode)
	}

	replayed := make(chan eventwire.Event, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 512)
		for {
			n, readErr := resp.Body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				if strings.Contains(string(buf), `"kind":"approval_request"`) {
					frame := string(buf)
					start := strings.Index(frame, "data: ")
					if start < 0 {
						return
					}
					end := strings.IndexByte(frame[start:], '\n')
					if end < 0 {
						end = len(frame) - start
					}
					var wire eventwire.Event
					if json.Unmarshal([]byte(strings.TrimSpace(frame[start+len("data: "):start+end])), &wire) == nil {
						replayed <- wire
					}
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	var approval eventwire.Event
	select {
	case approval = <-replayed:
	case <-time.After(2 * time.Second):
		t.Fatal("late SSE attach never received replayed approval_request")
	}
	if approval.Kind != "approval_request" || approval.Approval == nil || approval.Approval.Tool != "serve_write" {
		t.Fatalf("replayed approval = %+v, want serve_write approval_request", approval)
	}

	payload, err := json.Marshal(map[string]any{"id": approval.Approval.ID, "allow": true})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/approve", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	answer, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	answer.Body.Close()
	if answer.StatusCode != http.StatusNoContent {
		t.Fatalf("/approve status = %d", answer.StatusCode)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("executor run after approval: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not finish after approval")
	}
}
