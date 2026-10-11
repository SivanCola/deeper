package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func isolateUserConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"DEEPZORA_HOME", "DEEPZORA_STATE_HOME", "DEEPZORA_CACHE_HOME"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}
	t.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	return home
}

// setRuntimeGOOS overrides the package-level runtimeGOOS for one test. The
// t.Setenv call is a guard: it panics if the test also uses t.Parallel, which
// would otherwise race on the shared global.
func setRuntimeGOOS(t *testing.T, goos string) {
	t.Helper()
	t.Setenv("DEEPZORA_TEST_GOOS", goos)
	old := runtimeGOOS
	runtimeGOOS = goos
	t.Cleanup(func() { runtimeGOOS = old })
}

func expectedDefaultDeepzoraHome(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming", "deepzora")
	}
	return filepath.Join(home, ".deepzora")
}

func TestUserConfigDisplayPathCollapsesHome(t *testing.T) {
	home := isolateUserConfigHome(t)
	got := userConfigDisplayPath()
	if !strings.HasPrefix(got, "~/") {
		t.Fatalf("display path = %q, want ~/ prefix", got)
	}
	if !strings.HasSuffix(got, "deepzora/config.toml") {
		t.Fatalf("display path = %q, want deepzora/config.toml suffix", got)
	}
	if strings.Contains(got, home) {
		t.Fatalf("display path %q must not embed the absolute home", got)
	}
}

func TestUserConfigPathUsesDeepzoraHome(t *testing.T) {
	home := isolateUserConfigHome(t)
	want := filepath.Join(expectedDefaultDeepzoraHome(home), "config.toml")
	if got := UserConfigPath(); filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("UserConfigPath() = %q, want %q", got, want)
	}
}

func TestUserConfigPathHonorsDeepzoraHome(t *testing.T) {
	home := isolateUserConfigHome(t)
	custom := filepath.Join(home, "custom-home")
	t.Setenv("DEEPZORA_HOME", custom)

	want := filepath.Join(custom, "config.toml")
	if got := UserConfigPath(); filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("UserConfigPath() = %q, want %q", got, want)
	}
}

func TestLoadForRootUsesWindowsHomeFallbackWhenConfigDirUnavailable(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	oldGOOS := runtimeGOOS
	oldConfigDir := osUserConfigDir
	oldHomeDir := osUserHomeDir
	runtimeGOOS = "windows"
	osUserConfigDir = func() string { return "" }
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() {
		runtimeGOOS = oldGOOS
		osUserConfigDir = oldConfigDir
		osUserHomeDir = oldHomeDir
	})

	t.Setenv("DEEPZORA_HOME", "")

	configPath := filepath.Join(home, "AppData", "Roaming", "deepzora", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("default_model = \"custom/from-home\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot() error = %v", err)
	}
	if cfg.DefaultModel != "custom/from-home" {
		t.Fatalf("DefaultModel = %q, want %q", cfg.DefaultModel, "custom/from-home")
	}
}

func TestRenderTOMLHeaderShowsResolvedConfigPath(t *testing.T) {
	isolateUserConfigHome(t)
	out := RenderTOML(Default())
	want := "> " + userConfigDisplayPath() + " > built-in defaults."
	if !strings.Contains(out, want) {
		t.Fatalf("rendered header missing resolved config path %q", want)
	}
}

func TestWriteRootsForRootExcludesUserConfigDirByDefault(t *testing.T) {
	isolateUserConfigHome(t)
	project := t.TempDir()
	cfg := Default()

	roots := cfg.WriteRootsForRoot(project)
	want := filepath.Clean(filepath.Dir(UserConfigPath()))
	for _, root := range roots {
		if filepath.Clean(root) == want {
			t.Fatalf("WriteRootsForRoot() = %v, must not include user config dir %q by default", roots, want)
		}
	}
	if got := filepath.Clean(roots[0]); got != filepath.Clean(project) {
		t.Fatalf("first write root = %q, want project %q", got, project)
	}
}

func TestRenderTOMLDropsRetiredMCPPolicyFields(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`[[plugins]]
name = "github"
command = "github-mcp"
trusted_read_only_tools = ["issue_read", "pull_request_read"]
default_tools_approval_mode = "writes"
approvals_reviewer = "auto_review"

[plugins.tools.wipe]
approval_mode = "prompt"
`, &cfg); err != nil {
		t.Fatalf("legacy config should still decode: %v", err)
	}

	rendered := RenderTOML(&cfg)
	for _, retired := range []string{"trusted_read_only_tools", "default_tools_approval_mode", "approvals_reviewer", "\napproval_mode ="} {
		if strings.Contains(rendered, retired) {
			t.Fatalf("rendered config retained retired MCP field %q:\n%s", retired, rendered)
		}
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n%s", err, rendered)
	}
}

func TestRenderTOMLPreservesMCPTimeouts(t *testing.T) {
	cfg := Default()
	cfg.Tools.MCPCallTimeoutSeconds = intPtr(450)
	cfg.Tools.MCPStartupTimeoutSeconds = intPtr(45)
	cfg.Plugins = []PluginEntry{{
		Name:                  "maker",
		Command:               "maker-mcp",
		StartupTimeoutSeconds: 60,
		CallTimeoutSeconds:    600,
		ToolTimeoutSeconds: map[string]int{
			"generate/video": 1800,
			"search":         120,
		},
	}}

	rendered := RenderTOML(cfg)
	for _, want := range []string{
		"mcp_call_timeout_seconds = 450",
		"mcp_startup_timeout_seconds = 45",
		"startup_timeout_seconds = 60",
		"call_timeout_seconds = 600",
		`tool_timeout_seconds = { "generate/video" = 1800, "search" = 120 }`,
		"Raw MCP tool names",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, rendered)
		}
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n%s", err, rendered)
	}
	if got.Tools.MCPCallTimeoutSeconds == nil || *got.Tools.MCPCallTimeoutSeconds != 450 {
		t.Fatalf("MCPCallTimeoutSeconds round trip = %v, want 450", got.Tools.MCPCallTimeoutSeconds)
	}
	if got.Tools.MCPStartupTimeoutSeconds == nil || *got.Tools.MCPStartupTimeoutSeconds != 45 {
		t.Fatalf("MCPStartupTimeoutSeconds round trip = %v, want 45", got.Tools.MCPStartupTimeoutSeconds)
	}
	if got.Plugins[0].StartupTimeoutSeconds != 60 {
		t.Fatalf("StartupTimeoutSeconds round trip = %d, want 60", got.Plugins[0].StartupTimeoutSeconds)
	}
	if got.Plugins[0].CallTimeoutSeconds != 600 {
		t.Fatalf("CallTimeoutSeconds round trip = %d, want 600", got.Plugins[0].CallTimeoutSeconds)
	}
	if !reflect.DeepEqual(got.Plugins[0].ToolTimeoutSeconds, cfg.Plugins[0].ToolTimeoutSeconds) {
		t.Fatalf("ToolTimeoutSeconds round trip = %v, want %v", got.Plugins[0].ToolTimeoutSeconds, cfg.Plugins[0].ToolTimeoutSeconds)
	}
}

func TestRenderTOMLCreationLayoutStyle(t *testing.T) {
	c := Default()
	if err := c.SetDesktopLayoutStyle("creation"); err != nil {
		t.Fatalf("SetDesktopLayoutStyle: %v", err)
	}
	rendered := RenderTOML(c)
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n---\n%s", err, rendered)
	}
	if got.Desktop.LayoutStyle != "creation" {
		t.Errorf("desktop.layout_style = %q, want creation", got.Desktop.LayoutStyle)
	}
	if got.DesktopLayoutStyle() != "creation" {
		t.Errorf("DesktopLayoutStyle() = %q, want creation", got.DesktopLayoutStyle())
	}
}

func TestScopedRenderPreservesLSPConfig(t *testing.T) {
	const src = `
config_version = 12
default_model = "mimo"

[lsp]
enabled = true

[lsp.servers.lua]
command = "lua-language-server"
args = ["--stdio"]
env = { LUA_PATH = "./?.lua" }
language_id = "lua"
extensions = [".lua", ".script", ".gui_script"]
install_hint = "install lua-language-server"

[lsp.servers."c++"]
command = "clangd"
extensions = [".cc", ".cpp", ".hpp"]
`

	var cfg Config
	if _, err := toml.Decode(src, &cfg); err != nil {
		t.Fatalf("decode source TOML: %v", err)
	}

	for _, scope := range []RenderScope{RenderScopeFull, RenderScopeUser, RenderScopeProject} {
		t.Run(string(scope), func(t *testing.T) {
			rendered := RenderTOMLForScope(&cfg, scope)
			if !strings.Contains(rendered, "[lsp]") {
				t.Fatalf("render missing [lsp]:\n%s", rendered)
			}
			if !strings.Contains(rendered, "[lsp.servers.lua]") {
				t.Fatalf("render missing [lsp.servers.lua]:\n%s", rendered)
			}
			if !strings.Contains(rendered, `[lsp.servers."c++"]`) {
				t.Fatalf("render missing quoted c++ server key:\n%s", rendered)
			}

			var got Config
			if _, err := toml.Decode(rendered, &got); err != nil {
				t.Fatalf("decode rendered TOML: %v\n---\n%s", err, rendered)
			}
			if !got.LSP.Enabled {
				t.Fatalf("lsp.enabled = false, want true")
			}
			lua, ok := got.LSP.Servers["lua"]
			if !ok {
				t.Fatalf("lsp.servers.lua missing after round-trip: %+v", got.LSP.Servers)
			}
			if lua.Command != "lua-language-server" || lua.LanguageID != "lua" || lua.InstallHint != "install lua-language-server" {
				t.Fatalf("lsp.servers.lua scalar fields not preserved: %+v", lua)
			}
			if len(lua.Args) != 1 || lua.Args[0] != "--stdio" {
				t.Fatalf("lsp.servers.lua.args = %v, want [--stdio]", lua.Args)
			}
			if lua.Env["LUA_PATH"] != "./?.lua" {
				t.Fatalf("lsp.servers.lua.env = %v, want LUA_PATH", lua.Env)
			}
			if len(lua.Extensions) != 3 || lua.Extensions[0] != ".lua" || lua.Extensions[2] != ".gui_script" {
				t.Fatalf("lsp.servers.lua.extensions = %v", lua.Extensions)
			}
			cpp, ok := got.LSP.Servers["c++"]
			if !ok {
				t.Fatalf("lsp.servers.c++ missing after round-trip: %+v", got.LSP.Servers)
			}
			if cpp.Command != "clangd" || len(cpp.Extensions) != 3 || cpp.Extensions[1] != ".cpp" {
				t.Fatalf("lsp.servers.c++ not preserved: %+v", cpp)
			}
		})
	}
}

func BenchmarkRenderTOMLWithLSPServers(b *testing.B) {
	cfg := Default()
	cfg.LSP.Servers = make(map[string]LSPServer, 64)
	for i := range 64 {
		lang := "lang" + strconv.Itoa(i)
		cfg.LSP.Servers[lang] = LSPServer{
			Command:     "server-" + strconv.Itoa(i),
			Args:        []string{"--stdio", "--flag"},
			Env:         map[string]string{"SERVER_MODE": "stdio", "SERVER_ROOT": "."},
			LanguageID:  lang,
			Extensions:  []string{"." + lang, "." + lang + "x"},
			InstallHint: "install server-" + strconv.Itoa(i),
		}
	}

	b.ReportAllocs()
	for range b.N {
		rendered := RenderTOML(cfg)
		if len(rendered) == 0 {
			b.Fatal("empty render")
		}
	}
}

func TestNotificationsDefaultsKeepEventSwitchesEnabled(t *testing.T) {
	cfg := Default()
	if cfg.Notifications.Enabled {
		t.Fatal("notifications.enabled default = true, want false")
	}
	if !cfg.Notifications.TurnDone || !cfg.Notifications.ApprovalRequest || !cfg.Notifications.AskRequest {
		t.Fatalf("notification event switches default off: %+v", cfg.Notifications)
	}

	if _, err := toml.Decode("[notifications]\nenabled = true\n", cfg); err != nil {
		t.Fatalf("decode notifications: %v", err)
	}
	if !cfg.Notifications.Enabled || !cfg.Notifications.TurnDone || !cfg.Notifications.ApprovalRequest || !cfg.Notifications.AskRequest {
		t.Fatalf("enabled-only config should keep event switches on: %+v", cfg.Notifications)
	}
}

func TestProjectDeltaRendersToolsShellOverrides(t *testing.T) {
	c := Default()
	c.Tools.Shell.Prefer = "bash"
	c.Tools.Shell.Path = "/usr/local/bin/bash"

	delta := RenderTOMLProjectDelta(c)
	for _, want := range []string{"[tools.shell]", `prefer = "bash"`, `path = "/usr/local/bin/bash"`} {
		if !strings.Contains(delta, want) {
			t.Fatalf("project delta missing %q:\n%s", want, delta)
		}
	}
	if strings.Contains(delta, "[tools]\n\n") {
		t.Fatalf("project delta should not emit an empty [tools] block:\n%s", delta)
	}

	got := Default()
	if _, err := toml.Decode(delta, got); err != nil {
		t.Fatalf("decode project delta: %v\n%s", err, delta)
	}
	if got.Tools.Shell.Prefer != "bash" || got.Tools.Shell.Path != "/usr/local/bin/bash" {
		t.Fatalf("tools.shell = %+v, want bash with path", got.Tools.Shell)
	}
}

func TestResponsesProviderModeRoundTripsInUserAndProjectRender(t *testing.T) {
	cfg := Default()
	cfg.Providers = append(cfg.Providers, ProviderEntry{
		Name: "responses-test", Kind: "responses", BaseURL: "https://example.com/v1",
		RequestURL: "https://example.com/v1/custom/responses",
		Model:      "model", APIKeyEnv: "RESPONSES_API_KEY",
		ResponsesMode: "stateful",
	})

	for _, rendered := range []string{RenderTOMLForScope(cfg, RenderScopeUser), RenderTOMLProjectDelta(cfg)} {
		if !strings.Contains(rendered, `responses_mode = "stateful"`) || strings.Contains(rendered, "responses_stateful") {
			t.Fatalf("responses settings missing from render:\n%s", rendered)
		}
		var decoded Config
		if _, err := toml.Decode(rendered, &decoded); err != nil {
			t.Fatalf("decode responses config: %v\n%s", err, rendered)
		}
		entry, ok := decoded.Provider("responses-test")
		if !ok || entry.RequestURL != "https://example.com/v1/custom/responses" || entry.ResponsesMode != "stateful" {
			t.Fatalf("responses settings did not round-trip: %+v, found=%v", entry, ok)
		}
	}
}

func TestProjectDeltaRendersUICursorShape(t *testing.T) {
	c := Default()
	c.UI.CursorShape = "block"

	delta := RenderTOMLProjectDelta(c)
	for _, want := range []string{"[ui]", `cursor_shape = "block"`} {
		if !strings.Contains(delta, want) {
			t.Fatalf("project delta missing %q:\n%s", want, delta)
		}
	}

	got := Default()
	if _, err := toml.Decode(delta, got); err != nil {
		t.Fatalf("decode project delta: %v\n%s", err, delta)
	}
	if got.UICursorShape() != "block" {
		t.Fatalf("ui.cursor_shape = %q, want block", got.UICursorShape())
	}
}

func TestShowTurnUsageDefaultsOnAndRendersFalseOverride(t *testing.T) {
	c := Default()
	if !c.UI.ShowTurnUsage {
		t.Fatal("ui.show_turn_usage should default to true")
	}

	c.UI.ShowTurnUsage = false
	delta := RenderTOMLProjectDelta(c)
	for _, want := range []string{"[ui]", "show_turn_usage = false"} {
		if !strings.Contains(delta, want) {
			t.Fatalf("project delta missing %q:\n%s", want, delta)
		}
	}

	got := Default()
	if _, err := toml.Decode(delta, got); err != nil {
		t.Fatalf("decode project delta: %v\n%s", err, delta)
	}
	if got.UI.ShowTurnUsage {
		t.Fatal("ui.show_turn_usage false override did not round-trip")
	}
}

func TestProjectRenderPreservesNonDefaultLegacySections(t *testing.T) {
	c := Default()
	c.UI.Theme = "light"
	c.Desktop.CloseBehavior = "quit"
	c.Network.ProxyMode = "custom"
	c.Network.Proxy.Server = "127.0.0.1"
	c.Network.Proxy.Port = 7890

	project := RenderTOMLForScope(c, RenderScopeProject)
	for _, want := range []string{"[ui]", `theme = "light"`, "[network]", `proxy_mode = "custom"`, `server = "127.0.0.1"`} {
		if !strings.Contains(project, want) {
			t.Fatalf("project render missing legacy/non-default %q:\n%s", want, project)
		}
	}
}

func TestRenderTOMLRoundTripsPerModelPrices(t *testing.T) {
	orig := Default()
	orig.Providers = []ProviderEntry{{
		Name:      "deepseek",
		Kind:      "openai",
		BaseURL:   "https://api.deepseek.com",
		Models:    []string{"deepseek-v4-flash", "deepseek-v4-pro"},
		Default:   "deepseek-v4-flash",
		APIKeyEnv: "DEEPSEEK_API_KEY",
		Prices:    DeepSeekV4PricesForCurrency("CNY"),
	}}

	var got Config
	if _, err := toml.Decode(RenderTOML(orig), &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	p, ok := got.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider missing after round trip")
	}
	if p.Prices["deepseek-v4-flash"].Input != 2 || p.Prices["deepseek-v4-pro"].Output != 27 {
		t.Fatalf("prices after round trip = %+v", p.Prices)
	}
}

func TestRenderTOMLRoundTripsModelVisionOverrides(t *testing.T) {
	on, off := true, false
	orig := Default()
	orig.Providers = []ProviderEntry{{Name: "custom", Kind: "openai", BaseURL: "https://proxy.example.com/v1", Models: []string{"text-only", "vision-model"}, Default: "text-only", VisionDetail: "low", ModelOverrides: map[string]ProviderModelOverride{"text-only": {Vision: &off}, "vision-model": {Vision: &on}}}}
	rendered := RenderTOML(orig)
	if strings.Contains(rendered, "vision_models") || !strings.Contains(rendered, "model_overrides") {
		t.Fatal("rendered retired capability format")
	}
	var got Config
	if _, err := decodeTOMLBytes([]byte(rendered), &got); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		model  string
		vision bool
	}{{"text-only", false}, {"vision-model", true}} {
		p, ok := got.ResolveModel("custom/" + tc.model)
		if !ok || EffectiveVision(p) != tc.vision || p.VisionDetail != "low" {
			t.Fatal("current model capability did not survive round trip")
		}
	}
}

func TestRenderTOMLRoundTripsProviderHeadersAndModelOverrides(t *testing.T) {
	orig := Default()
	orig.Providers = []ProviderEntry{{
		Name:      "gateway",
		Kind:      "openai",
		BaseURL:   "https://gateway.example/v1",
		Models:    []string{"deepseek-v4-flash", "plain-chat"},
		Default:   "plain-chat",
		APIKeyEnv: "GATEWAY_API_KEY",
		Headers: map[string]string{
			"HTTP-Referer": "https://app.example",
			"X-Title":      "Deepzora",
		},
		ExtraBody: map[string]any{
			"enable_thinking": true,
			"top_p":           0.8,
			"metadata": map[string]any{
				"mode": "fast",
			},
		},
		AuthHeader:      true,
		MaxOutputTokens: 16_384,
		ModelOverrides: map[string]ProviderModelOverride{
			"deepseek-v4-flash": {
				ReasoningProtocol: ReasoningProtocolDeepSeek,
				SupportedEfforts:  []string{"high", "max"},
				DefaultEffort:     "high",
				Vision:            boolPtr(false),
				ContextWindow:     262_144,
				MaxOutputTokens:   32_768,
			},
		},
	}}

	rendered := RenderTOML(orig)
	if !strings.Contains(rendered, `headers     = { HTTP-Referer = "https://app.example", X-Title = "Deepzora" }`) {
		t.Fatalf("rendered TOML missing headers:\n%s", rendered)
	}
	if !strings.Contains(rendered, `extra_body`) || !strings.Contains(rendered, `"enable_thinking" = true`) {
		t.Fatalf("rendered TOML missing extra_body:\n%s", rendered)
	}
	if !strings.Contains(rendered, `auth_header = true`) {
		t.Fatalf("rendered TOML missing auth_header:\n%s", rendered)
	}
	if !strings.Contains(rendered, `max_output_tokens = 16384`) || !strings.Contains(rendered, `model_overrides`) || !strings.Contains(rendered, `reasoning_protocol = "deepseek"`) || !strings.Contains(rendered, `context_window = 262144`) || !strings.Contains(rendered, `max_output_tokens = 32768`) {
		t.Fatalf("rendered TOML missing model overrides:\n%s", rendered)
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n%s", err, rendered)
	}
	p, ok := got.Provider("gateway")
	if !ok {
		t.Fatal("gateway provider missing after round trip")
	}
	if p.Headers["HTTP-Referer"] != "https://app.example" || p.Headers["X-Title"] != "Deepzora" {
		t.Fatalf("headers after round trip = %+v", p.Headers)
	}
	if p.ExtraBody["enable_thinking"] != true || p.ExtraBody["top_p"] != 0.8 {
		t.Fatalf("extra_body after round trip = %+v", p.ExtraBody)
	}
	if !p.AuthHeader {
		t.Fatal("auth_header after round trip = false, want true")
	}
	if p.MaxOutputTokens != 16_384 {
		t.Fatalf("provider max_output_tokens after round trip = %d, want 16384", p.MaxOutputTokens)
	}
	metadata, ok := p.ExtraBody["metadata"].(map[string]any)
	if !ok || metadata["mode"] != "fast" {
		t.Fatalf("extra_body metadata after round trip = %+v", p.ExtraBody["metadata"])
	}
	ov := p.ModelOverrides["deepseek-v4-flash"]
	if ov.ReasoningProtocol != ReasoningProtocolDeepSeek || !reflect.DeepEqual(ov.SupportedEfforts, []string{"high", "max"}) || ov.DefaultEffort != "high" || ov.Vision == nil || *ov.Vision || ov.ContextWindow != 262_144 || ov.MaxOutputTokens != 32_768 {
		t.Fatalf("model override after round trip = %+v", ov)
	}

	// Older releases do not know context_window/max_output_tokens inside model
	// overrides, but their TOML decoder must still accept this release's config.
	type legacyModelOverride struct {
		ReasoningProtocol string   `toml:"reasoning_protocol"`
		SupportedEfforts  []string `toml:"supported_efforts"`
		DefaultEffort     string   `toml:"default_effort"`
		Vision            *bool    `toml:"vision"`
	}
	type legacyProvider struct {
		ModelOverrides map[string]legacyModelOverride `toml:"model_overrides"`
	}
	var legacy struct {
		Providers []legacyProvider `toml:"providers"`
	}
	if _, err := toml.Decode(rendered, &legacy); err != nil {
		t.Fatalf("legacy config shape cannot read per-model context window: %v", err)
	}
}

func TestRenderStringMapQuotesNonBareTOMLKeys(t *testing.T) {
	rendered := renderStringMap(map[string]string{
		"github:gh-fix-ci": "deepseek-pro",
		"review":           "deepseek-flash",
	})
	if !strings.Contains(rendered, `"github:gh-fix-ci" = "deepseek-pro"`) {
		t.Fatalf("non-bare key was not quoted: %s", rendered)
	}
	var got struct {
		M map[string]string `toml:"m"`
	}
	if _, err := toml.Decode("m = "+rendered, &got); err != nil {
		t.Fatalf("rendered inline map does not parse: %v (%s)", err, rendered)
	}
	if got.M["github:gh-fix-ci"] != "deepseek-pro" || got.M["review"] != "deepseek-flash" {
		t.Fatalf("decoded map = %+v", got.M)
	}
}

func TestDesktopExternalOpenerUserScopeRoundTrip(t *testing.T) {
	cfg := Default()
	if err := cfg.SetDesktopExternalOpener("ghostty"); err != nil {
		t.Fatal(err)
	}
	rendered := RenderTOMLForScope(cfg, RenderScopeUser)
	if !strings.Contains(rendered, `external_opener = "ghostty"`) {
		t.Fatalf("user config omitted desktop external opener:\n%s", rendered)
	}
	if project := RenderTOMLForScope(cfg, RenderScopeProject); strings.Contains(project, "external_opener") {
		t.Fatalf("project config leaked user-only external opener:\n%s", project)
	}
	var decoded Config
	if _, err := toml.Decode(rendered, &decoded); err != nil {
		t.Fatalf("decode rendered user config: %v", err)
	}
	if got := decoded.DesktopExternalOpener(); got != "ghostty" {
		t.Fatalf("round-trip external opener = %q, want ghostty", got)
	}
}

func TestRenderTOMLTablePathQuotesEachSegment(t *testing.T) {
	got := renderTOMLTablePath("lsp", "servers", "c++", "github:gh-fix-ci")
	want := `lsp.servers."c++"."github:gh-fix-ci"`
	if got != want {
		t.Fatalf("renderTOMLTablePath = %q, want %q", got, want)
	}
}

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }

func TestRenderTOMLConversationWidthRoundTrip(t *testing.T) {
	c := Default()
	if err := c.SetDesktopConversationWidth("full"); err != nil {
		t.Fatalf("SetDesktopConversationWidth: %v", err)
	}
	rendered := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(rendered, `conversation_width = "full"`) {
		t.Fatalf("rendered user config missing conversation_width:\n%s", rendered)
	}
	if project := RenderTOMLForScope(c, RenderScopeProject); strings.Contains(project, "conversation_width") {
		t.Fatalf("project config leaked user-only conversation_width:\n%s", project)
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n---\n%s", err, rendered)
	}
	if got.DesktopConversationWidth() != "full" {
		t.Fatalf("conversation_width after round trip = %q, want full", got.DesktopConversationWidth())
	}

	if err := c.SetDesktopConversationWidth("standard"); err != nil {
		t.Fatalf("reset conversation width: %v", err)
	}
	if rendered := RenderTOMLForScope(c, RenderScopeUser); strings.Contains(rendered, "conversation_width") {
		t.Fatalf("default conversation_width should be omitted:\n%s", rendered)
	}

	c.Desktop.ConversationWidth = " FULL "
	if rendered := RenderTOMLForScope(c, RenderScopeUser); !strings.Contains(rendered, `conversation_width = "full"`) {
		t.Fatalf("manually edited full width was not normalized:\n%s", rendered)
	}
}

func TestRenderTOMLDefaultStepsOmitted(t *testing.T) {
	isolateUserConfigHome(t)
	out := RenderTOML(Default())
	agentLines := extractSectionLines(out, "[agent]")
	for _, line := range agentLines {
		if strings.Contains(line, "max_steps") || strings.Contains(line, "planner_max_steps") {
			t.Errorf("default step limits should be hidden from generated config, got: %s", line)
		}
	}
}

func TestRenderTOMLWindowsSandboxDefaultAndExplicitEnforceDisabled(t *testing.T) {
	isolateUserConfigHome(t)
	setRuntimeGOOS(t, "windows")

	defaultRendered := RenderTOMLForScope(Default(), RenderScopeUser)
	if !strings.Contains(defaultRendered, `bash    = "off"`) {
		t.Fatalf("Windows default user config should render bash off:\n%s", defaultRendered)
	}

	cfg := Default()
	cfg.Sandbox.Bash = "enforce"
	delta := RenderTOMLProjectDelta(cfg)
	if strings.Contains(delta, `[sandbox]`) || strings.Contains(delta, `bash = `) {
		t.Fatalf("Windows explicit enforce should not render as an effective project delta:\n%s", delta)
	}
}

func extractSectionLines(toml, section string) []string {
	var lines []string
	inSection := false
	for line := range strings.SplitSeq(toml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, section) {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[") {
			break
		}
		if inSection {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func TestIsolatedHomeDirEmptyByDefault(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", "")
	if got := IsolatedHomeDir(); got != "" {
		t.Fatalf("IsolatedHomeDir() = %q, want empty", got)
	}
}

func TestIsolatedHomeDirReturnsCleanPath(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "isolated-deepzora")
	t.Setenv("DEEPZORA_HOME", raw)
	got := IsolatedHomeDir()
	if filepath.Clean(got) != filepath.Clean(raw) {
		t.Fatalf("IsolatedHomeDir() = %q, want %q", got, raw)
	}
}

func TestCacheDirHonorsDeepzoraHome(t *testing.T) {
	home := t.TempDir()
	isolated := filepath.Join(home, "isolated-home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEEPZORA_HOME", isolated)

	got := CacheDir()
	want := filepath.Join(isolated, "cache")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("CacheDir() = %q, want %q", got, want)
	}
}

func TestCacheDirHonorsDeepzoraCacheHomeOverDeepzoraHome(t *testing.T) {
	home := t.TempDir()
	cacheHome := filepath.Join(home, "custom-cache")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEEPZORA_HOME", filepath.Join(home, "isolated-home"))
	t.Setenv("DEEPZORA_CACHE_HOME", cacheHome)

	got := CacheDir()
	want := cacheHome
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("CacheDir() = %q, want %q (DEEPZORA_CACHE_HOME must win)", got, want)
	}
}

func TestUserConfigLoadPathNoLegacyFallbackWhenIsolated(t *testing.T) {
	home := isolateUserConfigHome(t)
	isolated := filepath.Join(home, "isolated-home")
	t.Setenv("DEEPZORA_HOME", isolated)

	// Create a legacy config at the OS production path — it must not be loaded.
	productionHome := expectedDefaultDeepzoraHome(home)
	if err := os.MkdirAll(productionHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productionHome, "config.toml"), []byte("default_model = \"production/model\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The primary config under isolated home does not exist yet.
	got := userConfigLoadPath()
	want := filepath.Join(isolated, "config.toml")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("userConfigLoadPath() = %q, want %q (must not fall back to production legacy config)", got, want)
	}
}

func TestCredentialSourceCandidatesSkipHomeEnvWhenIsolated(t *testing.T) {
	isolateUserConfigHome(t)
	t.Setenv("DEEPZORA_HOME", filepath.Join(t.TempDir(), "isolated-home"))

	// Write a key into the production home .env — it must not appear as a source.
	if home, err := os.UserHomeDir(); err == nil {
		if err := os.WriteFile(filepath.Join(home, ".env"), []byte("LEAKED_KEY=leaked-value\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	candidates := credentialSourceCandidates(".")
	for _, c := range candidates {
		if c.Kind == CredentialSourceHomeEnv {
			t.Fatalf("credentialSourceCandidates includes CredentialSourceHomeEnv when isolated: %v", c)
		}
	}
}

// TestProjectConfigCannotOverrideSecrets pins [secrets] as a user-global
// security control: a cloned repository's deepzora.toml must not be able to
// opt the user into subprocess env stripping or sensitive-path hiding.
func TestProjectConfigCannotOverrideSecrets(t *testing.T) {
	isolateUserConfigHome(t)
	t.Setenv("DEEPZORA_HOME", "")
	globalDir := filepath.Dir(UserConfigPath())
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalTOML := "[secrets]\nfilter_subprocess_env = false\nprotect_sensitive_files = false\n"
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(globalTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	project := t.TempDir()
	projectTOML := "[secrets]\nfilter_subprocess_env = true\nprotect_sensitive_files = true\n"
	if err := os.WriteFile(filepath.Join(project, "deepzora.toml"), []byte(projectTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot() error = %v", err)
	}
	if cfg.Secrets.FilterSubprocessEnv {
		t.Error("project deepzora.toml enabled filter_subprocess_env; [secrets] must stay user-global")
	}
	if cfg.Secrets.ProtectSensitiveFiles {
		t.Error("project deepzora.toml enabled protect_sensitive_files; [secrets] must stay user-global")
	}
}

// TestRenderTOMLPersistsSecretsSection pins config-save round-tripping: the
// renderer must emit [secrets] for the user scope or every WriteFile would
// silently drop the user's security toggles.
func TestRenderTOMLPersistsSecretsSection(t *testing.T) {
	cfg := Default()
	cfg.Secrets.FilterSubprocessEnv = true
	cfg.Secrets.ProtectSensitiveFiles = true

	out := RenderTOMLForScope(cfg, RenderScopeUser)
	for _, want := range []string{"[secrets]", "filter_subprocess_env = true", "protect_sensitive_files = true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("user-scope render missing %q:\n%s", want, out)
		}
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	back := Default()
	if err := mergeFile(back, path); err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}
	if !back.Secrets.FilterSubprocessEnv || !back.Secrets.ProtectSensitiveFiles {
		t.Fatalf("secrets toggles lost in render round-trip: %+v", back.Secrets)
	}

	// Project scope must not render the section — LoadForRoot ignores it there.
	if proj := RenderTOMLForScope(cfg, RenderScopeProject); strings.Contains(proj, "[secrets]") {
		t.Fatalf("project scope rendered [secrets]:\n%s", proj)
	}
	if strings.Contains(out, "redact_tool_output") {
		t.Fatalf("user-scope render still exposes removed live-redaction setting:\n%s", out)
	}
}
