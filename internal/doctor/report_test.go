package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/session"
)

func TestRedactHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)        // os.UserHomeDir on unix
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on windows
	sep := string(os.PathSeparator)

	if got := redactHome(home); got != "~" {
		t.Fatalf("home itself: got %q, want ~", got)
	}
	under := filepath.Join(home, "projects", "x")
	if got, want := redactHome(under), "~"+sep+"projects"+sep+"x"; got != want {
		t.Fatalf("under home: got %q, want %q", got, want)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere") // sibling temp, not under home
	if got := redactHome(outside); got != outside {
		t.Fatalf("outside home must be unchanged: got %q", got)
	}
	if got := redactHome(""); got != "" {
		t.Fatalf("empty must stay empty: got %q", got)
	}
}

func TestCollectReportRedactsSecrets(t *testing.T) {
	t.Setenv("DEEPZORA_TEST_SECRET", "sk-live-secret")

	cfg := config.Default()
	cfg.DefaultModel = "custom"
	cfg.Providers = []config.ProviderEntry{{
		Name:      "custom",
		Kind:      "openai",
		BaseURL:   "https://api.example.com/v1?token=secret-query",
		Model:     "model-a",
		APIKeyEnv: "DEEPZORA_TEST_SECRET",
	}}
	cfg.Plugins = []config.PluginEntry{{
		Name:    "remote",
		Type:    "http",
		URL:     "https://mcp.example.com/path?api_key=secret-query",
		Headers: map[string]string{"Authorization": "Bearer sk-live-secret"},
	}}
	cfg.Network = config.NetworkConfig{
		ProxyMode: "custom",
		Proxy: config.NetworkProxyConfig{
			Type:     "socks5",
			Server:   "proxy.example.com",
			Port:     1080,
			Username: "proxy-user",
			Password: "proxy-secret",
		},
	}

	report := Collect(Options{Version: "test-version", Config: cfg})
	text := RenderText(report)
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	combined := text + "\n" + string(raw)

	for _, secret := range []string{"sk-live-secret", "secret-query", "Authorization", "proxy-secret"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("doctor report leaked %q:\n%s", secret, combined)
		}
	}
	if !strings.Contains(combined, "api.example.com") || !strings.Contains(combined, "mcp.example.com") {
		t.Fatalf("doctor report should keep useful host diagnostics:\n%s", combined)
	}
}

func TestCollectReportDoesNotRequireAPIKey(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", filepath.Join(t.TempDir(), "deepzora"))
	t.Setenv("DEEPSEEK_API_KEY", "")

	cfg := config.Default()
	report := Collect(Options{Version: "1.2.3", Config: cfg})
	text := RenderText(report)

	if report.Version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", report.Version)
	}
	if len(report.Providers) == 0 {
		t.Fatal("expected built-in providers in report")
	}
	if report.Providers[0].KeyPresent {
		t.Fatal("provider key should be reported missing when env is empty")
	}
	if !strings.Contains(text, "deepzora 1.2.3 doctor") {
		t.Fatalf("text report missing header:\n%s", text)
	}
	if !strings.Contains(text, "missing") {
		t.Fatalf("text report should mention missing key state:\n%s", text)
	}
}

func TestCollectCurrentSessionsIsReadOnlyAndRedacted(t *testing.T) {
	dir := t.TempDir()
	persistence := session.NewFilesystemPersistence(dir)
	for i := range 101 {
		store, err := persistence.Create(session.CreateOptions{SessionID: fmt.Sprintf("private-session-%03d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	attachmentDir := filepath.Join(dir, "private-session-000", "attachments")
	if err := os.MkdirAll(attachmentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachmentDir, "secret-message.txt"), []byte("secret message"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Unrelated files and external query caches are not session-owned data.
	if err := os.WriteFile(filepath.Join(dir, "unrelated.jsonl"), []byte("private obsolete data"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() map[string]string {
		files := map[string]string{}
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				files[path] = "directory"
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = fmt.Sprintf("%d:%s", info.ModTime().UnixNano(), body)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := fingerprint()
	report := collectSessions(dir)
	if report.Count != 101 || report.Bytes == 0 || report.Error != "" {
		t.Fatalf("current storage report = %+v", report)
	}
	if after := fingerprint(); !reflect.DeepEqual(before, after) {
		t.Fatal("diagnostic read changed storage or created a query cache")
	}
	firstBytes := report.Bytes
	if err := os.WriteFile(filepath.Join(attachmentDir, "more.bin"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := collectSessions(dir); got.Bytes != firstBytes+5 {
		t.Fatalf("owned attachment bytes = %d, want %d", got.Bytes, firstBytes+5)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret message", "private-session", "secret-message", "private obsolete data", "recovery"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("aggregate diagnostics leaked %q: %s", secret, raw)
		}
	}
}

func TestCollectCurrentSessionsMissingStoreDoesNotCreateIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	if report := collectSessions(dir); report.Count != 0 || report.Bytes != 0 || report.Error != "" {
		t.Fatalf("absent store = %+v", report)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("diagnostics created store: %v", err)
	}
}

func TestRenderTextSurfacesWarningsUpTop(t *testing.T) {
	text := RenderText(Report{Warnings: []string{"config deepzora.toml: parse boom"}})
	w := strings.Index(text, "parse boom")
	if w < 0 {
		t.Fatalf("warning missing from report:\n%s", text)
	}
	if p := strings.Index(text, "\nproviders\n"); p >= 0 && w > p {
		t.Fatalf("warning should appear before the providers section, not buried below:\n%s", text)
	}
}

func TestRenderTextFlagsUnavailableSandboxAsFailClosed(t *testing.T) {
	inactive := RenderText(Report{Sandbox: SandboxReport{Bash: "enforce", Available: false}})
	if !strings.Contains(inactive, "bash execution is refused") {
		t.Fatalf("enforce without an OS sandbox should report fail-closed bash behavior:\n%s", inactive)
	}
	if strings.Contains(inactive, "runs unconfined") {
		t.Fatalf("enforce without an OS sandbox should not claim bash runs unconfined:\n%s", inactive)
	}

	active := RenderText(Report{Sandbox: SandboxReport{Bash: "enforce", Available: true}})
	if strings.Contains(active, "bash execution is refused") {
		t.Fatalf("enforce with an OS sandbox should not be flagged unavailable:\n%s", active)
	}
}

// TestCollectFlagsIgnoredEnforceConfig pins the visibility contract for the
// platform force-off: when the config file says enforce but the effective mode
// resolves to off (Windows), doctor must say so in both the warnings list and
// the sandbox bash line instead of silently reporting "off".
func TestCollectFlagsIgnoredEnforceConfig(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", filepath.Join(t.TempDir(), "deepzora"))

	cfg := config.Default()
	cfg.Sandbox.Bash = "enforce"
	report := Collect(Options{Version: "test", Config: cfg})

	ignored := cfg.BashMode() == "off"
	if report.Sandbox.BashConfigIgnored != ignored {
		t.Fatalf("BashConfigIgnored = %v, want %v (BashMode %q)", report.Sandbox.BashConfigIgnored, ignored, cfg.BashMode())
	}

	text := RenderText(Report{Sandbox: SandboxReport{Bash: "off", BashConfigIgnored: true}})
	if !strings.Contains(text, `config requests "enforce", ignored`) {
		t.Fatalf("ignored enforce should be flagged on the bash line:\n%s", text)
	}
	plain := RenderText(Report{Sandbox: SandboxReport{Bash: "off"}})
	if strings.Contains(plain, "ignored") {
		t.Fatalf("plain off must not claim the config was ignored:\n%s", plain)
	}
}

func TestHomeIsolationWarningDetectsMismatch(t *testing.T) {
	// Prefer a synthetic mismatch without requiring root privileges.
	acct, err := user.Current()
	if err != nil || acct == nil || strings.TrimSpace(acct.HomeDir) == "" {
		t.Skip("account home unavailable")
	}
	t.Setenv("DEEPZORA_HOME", "")
	serviceHome := filepath.Join(t.TempDir(), "service-home")
	t.Setenv("HOME", serviceHome)
	t.Setenv("USERPROFILE", serviceHome)
	got := homeIsolationWarning()
	if got == "" {
		t.Fatal("expected HOME mismatch warning")
	}
	if !strings.Contains(got, "DEEPZORA_HOME") {
		t.Fatalf("warning = %q, want DEEPZORA_HOME guidance", got)
	}
	// Shareable output must not embed either absolute home path.
	if strings.Contains(got, serviceHome) || strings.Contains(got, acct.HomeDir) {
		t.Fatalf("warning leaked a home path: %q", got)
	}
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	if got := homeIsolationWarning(); got != "" {
		t.Fatalf("DEEPZORA_HOME set should silence warning, got %q", got)
	}
}
