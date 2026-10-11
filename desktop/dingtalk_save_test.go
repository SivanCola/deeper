package main

import (
	"testing"

	"deepzora/internal/config"
)

// TestSetBotSettingsDingtalkRoundTrip 验证钉钉配置经 SetBotSettings 后能正确落盘并被读回。
// 这是排查「钉钉配置界面几个字段写进去无法保存」的回归测试。
func TestSetBotSettingsDingtalkRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Bot.Dingtalk.BotName = "InitialBot"
	cfg.Bot.Dingtalk.RequireMention = true
	cfg.Bot.Connections = []config.BotConnectionConfig{{ID: "ding-main", Provider: "dingtalk", Domain: "dingtalk", Enabled: true, Credential: config.BotConnectionCredential{AppID: "appkey-initial", AppSecretEnv: "DINGTALK_CLIENT_SECRET"}, SessionMappings: []config.BotConnectionSessionMapping{{RemoteID: "cid-preserved", Scope: "global", SessionID: "session-current", SessionSource: "auto"}}}}

	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save initial: %v", err)
	}

	app := NewApp()
	view := botSettingsView(cfg.Bot)
	view.Dingtalk.ClientID = "appkey-edited"
	view.Dingtalk.BotName = "EditedBot"
	view.Dingtalk.RequireMention = false
	view.Dingtalk.ClientSecretEnv = "DINGTALK_CLIENT_SECRET"
	if err := app.SetBotSettings(view); err != nil {
		t.Fatalf("SetBotSettings: %v", err)
	}

	got := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	if got.Bot.Connections[0].Credential.AppID != "appkey-edited" {
		t.Fatalf("clientId = %q, want appkey-edited", got.Bot.Connections[0].Credential.AppID)
	}
	if got.Bot.Dingtalk.BotName != "EditedBot" {
		t.Fatalf("botName = %q, want EditedBot", got.Bot.Dingtalk.BotName)
	}
	if got.Bot.Dingtalk.RequireMention {
		t.Fatalf("requireMention = true, want false")
	}
	if got.Bot.Connections[0].Credential.AppSecretEnv != "DINGTALK_CLIENT_SECRET" {
		t.Fatalf("secretEnv = %q, want DINGTALK_CLIENT_SECRET", got.Bot.Connections[0].Credential.AppSecretEnv)
	}
	if mappings := got.Bot.Connections[0].SessionMappings; len(mappings) != 1 || mappings[0].RemoteID != "cid-preserved" || mappings[0].SessionID != "session-current" {
		t.Fatalf("session mappings = %+v, want current connection binding preserved", mappings)
	}
}

func TestDingtalkRuntimeConnectionID(t *testing.T) {
	conn, id, ok := dingtalkRuntimeConnection([]config.BotConnectionConfig{{ID: "feishu", Provider: "feishu", Enabled: true}, {ID: "ding-current", Provider: "dingtalk", Enabled: true}})
	if !ok || id != "ding-current" || conn.ID != id {
		t.Fatalf("current connection = %+v %q %v", conn, id, ok)
	}
	for _, records := range [][]config.BotConnectionConfig{nil, {{ID: "disabled", Provider: "dingtalk"}}} {
		_, id, ok := dingtalkRuntimeConnection(records)
		if ok || id != "" {
			t.Fatalf("missing current connection = %q %v", id, ok)
		}
	}
}

// TestDingtalkRuntimeConnectionResolvesCredentials 验证测试发送凭据从所选
// connection 解析（而不是 legacy [bot.dingtalk] 块）——否则 connection 驱动的
// 运行时在 legacy 块为空时误报 dingtalk_secret_missing。
func TestDingtalkRuntimeConnectionResolvesCredentials(t *testing.T) {
	conns := []config.BotConnectionConfig{
		{ID: "feishu-feishu", Provider: "feishu", Enabled: true},
		{ID: "dingtalk-main", Provider: "dingtalk", Enabled: true,
			Credential: config.BotConnectionCredential{AppID: "app-from-conn", AppSecretEnv: "DINGTALK_CONN_SECRET"}},
	}
	conn, id, ok := dingtalkRuntimeConnection(conns)
	if !ok {
		t.Fatal("enabled dingtalk connection not found")
	}
	if id != "dingtalk-main" {
		t.Fatalf("runtime id = %q, want dingtalk-main", id)
	}
	if conn.Credential.AppID != "app-from-conn" || conn.Credential.AppSecretEnv != "DINGTALK_CONN_SECRET" {
		t.Fatalf("resolved connection credentials = %q/%q, want app-from-conn/DINGTALK_CONN_SECRET",
			conn.Credential.AppID, conn.Credential.AppSecretEnv)
	}
	// 未启用的钉钉连接不应命中。
	if _, _, ok := dingtalkRuntimeConnection([]config.BotConnectionConfig{
		{ID: "dingtalk-off", Provider: "dingtalk", Enabled: false},
	}); ok {
		t.Fatal("disabled dingtalk connection must not be selected")
	}
}

// TestSetBotSettingsDingtalkRuntimeOptionsRoundTrip 验证 legacy [bot.dingtalk]
// 的模型/工具审批模式/工作目录经 SetBotSettings 落盘并被读回。这是排查
// 「钉钉面板模型、权限切换无法保存」的回归测试：config 结构体与 settings
// view 都支持这些字段，但 render.go 漏写导致 SaveTo 丢弃（已被修复）。
func TestSetBotSettingsDingtalkRuntimeOptionsRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Bot.Dingtalk.BotName = "安博特"
	cfg.Bot.Dingtalk.RequireMention = true
	cfg.Bot.Connections = []config.BotConnectionConfig{{ID: "ding-main", Provider: "dingtalk", Domain: "dingtalk", Enabled: true, Credential: config.BotConnectionCredential{AppID: "ding-appkey", AppSecretEnv: "DINGTALK_CLIENT_SECRET"}, Model: "deepseek-flash/deepseek-flash", ToolApprovalMode: "danger-full-access", WorkspaceRoot: "/tmp/probe-ws"}}

	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save initial: %v", err)
	}

	app := NewApp()
	view := botSettingsView(cfg.Bot)
	if view.Dingtalk.Model != "deepseek-flash/deepseek-flash" {
		t.Fatalf("view model = %q, want deepseek-flash", view.Dingtalk.Model)
	}
	if view.Dingtalk.ToolApprovalMode != "danger-full-access" {
		t.Fatalf("view permission preset = %q, want danger-full-access", view.Dingtalk.ToolApprovalMode)
	}
	view.Dingtalk.Model = "deepseek-flash/deepseek-flash"
	view.Dingtalk.ToolApprovalMode = "danger-full-access"
	view.Dingtalk.WorkspaceRoot = "/tmp/probe-ws"
	if err := app.SetBotSettings(view); err != nil {
		t.Fatalf("SetBotSettings: %v", err)
	}

	got := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	if got.Bot.Connections[0].Model != "deepseek-flash/deepseek-flash" {
		t.Fatalf("model = %q, want deepseek-flash (render must persist)", got.Bot.Connections[0].Model)
	}
	if got.Bot.Connections[0].ToolApprovalMode != "danger-full-access" {
		t.Fatalf("toolApprovalMode = %q, want danger-full-access (render must persist)", got.Bot.Connections[0].ToolApprovalMode)
	}
	if got.Bot.Connections[0].WorkspaceRoot != "/tmp/probe-ws" {
		t.Fatalf("workspaceRoot = %q, want /tmp/probe-ws (render must persist)", got.Bot.Connections[0].WorkspaceRoot)
	}
	// 原有字段不能丢。
	if got.Bot.Connections[0].Credential.AppSecretEnv != "DINGTALK_CLIENT_SECRET" {
		t.Fatalf("connection credential reference lost: %+v", got.Bot.Connections[0].Credential)
	}
	if got.Bot.Dingtalk.BotName != "安博特" {
		t.Fatalf("botName lost: %q", got.Bot.Dingtalk.BotName)
	}
}
