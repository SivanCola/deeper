package boot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"

	_ "deepzora/internal/provider/openai"
	_ "deepzora/internal/tool/builtin"
)

// TestBuildUnknownModelErrorIsActionable: a default_model that doesn't resolve
// (e.g. a stale preset name after [[providers]] replaced the built-in presets) must
// fail with a message that names the model, lists what IS configured, and hints
// at the [[providers]] trap — not a silent empty model.
func TestBuildUnknownModelErrorIsActionable(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	dir := robustTempDir(t)
	fenceBootTestHistoryCatalog(t)
	t.Chdir(dir)
	writeFile(t, dir, "deepzora.toml", `
default_model = "legacy-missing"

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://example.invalid"
models = ["deepseek-v4-flash"]
api_key_env = "DEEPZORA_TEST_KEY_UNSET"
`)
	approveWorkspace(t, dir)

	_, err := Build(context.Background(), withTestSession(t, Options{Sink: event.Discard}))
	if err == nil {
		t.Fatal("expected an error for an unresolvable default_model")
	}
	msg := err.Error()
	for _, want := range []string{`"legacy-missing"`, "deepseek-flash", "[[providers]]"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q should mention %q", msg, want)
		}
	}
}

func TestBuildRejectsInvalidProjectModelDespiteValidUserDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	writeFile(t, home, "config.toml", `
default_model = "deepseek-pro"

[[providers]]
name = "deepseek-pro"
kind = "openai"
base_url = "https://example.invalid"
models = ["deepseek-v4-pro"]
api_key_env = "DEEPZORA_TEST_KEY_UNSET"
`)

	dir := robustTempDir(t)
	fenceBootTestHistoryCatalog(t)
	t.Chdir(dir)
	writeFile(t, dir, "deepzora.toml", `
default_model = "deepseek-flash"
`)
	approveWorkspace(t, dir)

	ctrl, err := Build(context.Background(), withTestSession(t, Options{Sink: event.Discard}))
	if ctrl != nil {
		ctrl.Close()
	}
	if err == nil || !strings.Contains(err.Error(), `unknown model "deepseek-flash"`) {
		t.Fatalf("Build error = %v, want explicit invalid project model", err)
	}
}

// TestBuildNoticesMissingAPIKey: a resolvable model whose API key env is unset
// builds fine (RequireKey is false so the UI stays reachable) but must emit a
// notice naming the env var, instead of silently showing a dead/empty model.
func TestBuildNoticesMissingAPIKey(t *testing.T) {
	const keyEnv = "DEEPZORA_MISSING_KEY_FOR_TEST"
	dir := robustTempDir(t)
	fenceBootTestHistoryCatalog(t)
	t.Chdir(dir)
	writeFile(t, dir, "deepzora.toml", `
default_model = "x"

[[providers]]
name = "x"
kind = "openai"
base_url = "https://example.invalid"
models = ["m"]
api_key_env = "`+keyEnv+`"
`)
	approveWorkspace(t, dir)

	var notices []event.Event
	ctrl, err := Build(context.Background(), withTestSession(t, Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.Notice {
				notices = append(notices, e)
			}
		}),
	}))
	if err != nil {
		t.Fatalf("Build should succeed with RequireKey=false even without a key: %v", err)
	}
	defer ctrl.Close()

	found := false
	for _, n := range notices {
		if n.Text == "Selected model is missing its API key." && strings.Contains(n.Detail, keyEnv) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a notice naming the unset key env %q; got %v", keyEnv, notices)
	}
}

func TestBuildClassifiesUnavailableCredentialStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	if err := os.Mkdir(filepath.Join(home, ".env"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, home, "config.toml", `
default_model = "relay/chat"

[[providers]]
name = "relay"
kind = "openai"
base_url = "https://example.invalid/v1"
models = ["chat"]
api_key_env = "RELAY_TEST_KEY"
`)
	dir := robustTempDir(t)
	fenceBootTestHistoryCatalog(t)
	t.Chdir(dir)

	ctrl, err := Build(context.Background(), withTestSession(t, Options{Sink: event.Discard}))
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	if got := ctrl.AuthenticationState(); got.Status != control.AuthenticationCredentialStoreUnavailable || got.Code != "credential_store_unavailable" {
		t.Fatalf("authentication state = %+v", got)
	}

	_, err = Build(context.Background(), withTestSession(t, Options{Sink: event.Discard, RequireKey: true, Model: "relay/chat"}))
	var authErr *control.AuthenticationError
	if !errors.As(err, &authErr) || authErr.State.Status != control.AuthenticationCredentialStoreUnavailable {
		t.Fatalf("headless build error = %v, want credential-store AuthenticationError", err)
	}
}

func TestBuildDoesNotNoticeMissingAPIKeyForNoAuthLoopback(t *testing.T) {
	const keyEnv = "DEEPZORA_LOCAL_GATEWAY_KEY_FOR_TEST"
	dir := robustTempDir(t)
	fenceBootTestHistoryCatalog(t)
	t.Chdir(dir)
	t.Setenv(keyEnv, "")
	writeFile(t, dir, "deepzora.toml", `
default_model = "local/model-a"

[[providers]]
name = "local"
kind = "openai"
base_url = "http://127.0.0.1:23333/v1"
models = ["model-a"]
api_key_env = "`+keyEnv+`"
`)
	approveWorkspace(t, dir)

	var notices []string
	ctrl, err := Build(context.Background(), withTestSession(t, Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.Notice {
				notices = append(notices, e.Text)
			}
		}),
	}))
	if err != nil {
		t.Fatalf("Build should allow no-auth loopback provider without a key: %v", err)
	}
	defer ctrl.Close()

	for _, n := range notices {
		if strings.Contains(n, keyEnv) {
			t.Fatalf("did not expect missing-key notice for loopback no-auth provider; got %v", notices)
		}
	}
}

// TestBuildKeylessDefaultFallsBackToConfiguredProvider: when the configured
// default_model is resolvable but its API key is missing, Build should fall
// through to the next provider that IS configured rather than fail with
// "missing env X_API_KEY" (issue #6996). The fallback only kicks in when
// the caller did not pass an explicit Options.Model — explicit choices
// still fail loudly so the user is not silently rerouted.
func TestBuildKeylessDefaultFallsBackToConfiguredProvider(t *testing.T) {
	const keylessEnv = "DEEPZORA_KEYLESS_DEFAULT_FALLBACK_KEYLESS"
	const configuredEnv = "DEEPZORA_KEYLESS_DEFAULT_FALLBACK_CONFIGURED"

	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	t.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	if _, err := config.SetCredential(configuredEnv, "sk-test"); err != nil {
		t.Fatalf("seed configured key: %v", err)
	}

	dir := robustTempDir(t)
	fenceBootTestHistoryCatalog(t)
	t.Chdir(dir)
	writeFile(t, dir, "deepzora.toml", `
default_model = "deepseek/deepseek-v4-flash"

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash"]
api_key_env = "`+keylessEnv+`"

[[providers]]
name = "audio"
kind = "openai"
base_url = "https://audio.example.com/v1"
models = ["tts-1"]
api_key_env = "`+configuredEnv+`"

[[providers]]
name = "minimax"
kind = "openai"
base_url = "https://api.MiniMax.chat/v1"
models = ["MiniMax-M3"]
api_key_env = "`+configuredEnv+`"
`)
	approveWorkspace(t, dir)

	ctrl, err := Build(context.Background(), withTestSession(t, Options{Sink: event.Discard}))
	if err != nil {
		t.Fatalf("Build should fall back to a configured provider instead of failing on the keyless default: %v", err)
	}
	defer ctrl.Close()
	if got, want := ctrl.Label(), "MiniMax-M3"; got != want {
		t.Fatalf("controller label = %q, want %q (the next provider's model with a configured key)", got, want)
	}
}

// TestBuildExplicitKeylessModelStillFails: an Options.Model that the user
// passed explicitly must keep its fail-fast behavior even when another
// provider is configured and could be used as a fallback. The user asked
// for that specific ref, so we must not silently reroute them.
func TestBuildExplicitKeylessModelStillFails(t *testing.T) {
	const keylessEnv = "DEEPZORA_EXPLICIT_KEYLESS_KEY"
	const configuredEnv = "DEEPZORA_EXPLICIT_KEYLESS_CONFIGURED"

	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	t.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	if _, err := config.SetCredential(configuredEnv, "sk-test"); err != nil {
		t.Fatalf("seed configured key: %v", err)
	}

	dir := robustTempDir(t)
	fenceBootTestHistoryCatalog(t)
	t.Chdir(dir)
	writeFile(t, dir, "deepzora.toml", `
default_model = "minimax/MiniMax-M3"

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash"]
api_key_env = "`+keylessEnv+`"

[[providers]]
name = "minimax"
kind = "openai"
base_url = "https://api.MiniMax.chat/v1"
models = ["MiniMax-M3"]
api_key_env = "`+configuredEnv+`"
`)
	approveWorkspace(t, dir)

	_, err := Build(context.Background(), withTestSession(t, Options{
		Sink:       event.Discard,
		Model:      "deepseek/deepseek-v4-flash",
		RequireKey: true,
	}))
	if err == nil {
		t.Fatal("explicit keyless ref must fail loudly even with a configured fallback available")
	}
	if !strings.Contains(err.Error(), keylessEnv) {
		t.Fatalf("error %q should mention %q", err.Error(), keylessEnv)
	}
}
