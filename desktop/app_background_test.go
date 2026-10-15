package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/billing"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/jobs"
)

func TestBeforeCloseAllowsSystemQuitWhenBackgroundCloseEnabled(t *testing.T) {
	isolateDesktopUserDirs(t)
	consumeSystemQuitRequested()
	t.Cleanup(func() { consumeSystemQuitRequested() })

	userCfg := config.LoadForEdit(config.UserConfigPath())
	if err := userCfg.SetDesktopCloseBehavior("background"); err != nil {
		t.Fatal(err)
	}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}

	markSystemQuitRequested()
	if prevent := NewApp().beforeClose(context.Background()); prevent {
		t.Fatal("system quit should bypass background close-to-tray behavior")
	}
	if consumeSystemQuitRequested() {
		t.Fatal("system quit marker should be consumed by beforeClose")
	}
}

func TestBackgroundCloseHideStrategyByPlatform(t *testing.T) {
	tests := []struct {
		goos string
		want bool
	}{
		{goos: "darwin", want: true},
		{goos: "windows", want: false},
		{goos: "linux", want: false},
		{goos: "freebsd", want: false},
	}
	for _, tt := range tests {
		if got := backgroundCloseUsesApplicationHide(tt.goos); got != tt.want {
			t.Fatalf("backgroundCloseUsesApplicationHide(%q) = %v, want %v", tt.goos, got, tt.want)
		}
	}
}

func TestBackgroundCloseRequiresRestorePath(t *testing.T) {
	tests := []struct {
		name        string
		goos        string
		trayStarted bool
		trayReady   bool
		want        bool
	}{
		{name: "macOS restores from Dock", goos: "darwin", trayStarted: false, trayReady: false, want: true},
		{name: "Windows tray ready", goos: "windows", trayStarted: true, trayReady: true, want: true},
		{name: "Windows tray started but not ready", goos: "windows", trayStarted: true, trayReady: false, want: false},
		{name: "Linux tray ready", goos: "linux", trayStarted: true, trayReady: true, want: true},
		{name: "Linux tray started but not ready", goos: "linux", trayStarted: true, trayReady: false, want: false},
		{name: "Linux no tray", goos: "linux", trayStarted: false, trayReady: false, want: false},
		{name: "other Unix no tray", goos: "freebsd", trayStarted: false, trayReady: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backgroundCloseHasRestorePathFor(tt.goos, tt.trayStarted, tt.trayReady); got != tt.want {
				t.Fatalf("backgroundCloseHasRestorePathFor(%q, %v, %v) = %v, want %v", tt.goos, tt.trayStarted, tt.trayReady, got, tt.want)
			}
		})
	}
}

func TestBackgroundCloseReadySignalRequiresCurrentReadyState(t *testing.T) {
	app := NewApp()
	tray := newDesktopTray()
	app.mu.Lock()
	app.tray = tray
	app.mu.Unlock()

	if app.waitForTrayReady(0) {
		t.Fatal("tray should not be ready before its ready signal")
	}

	tray.markReady()
	if app.waitForTrayReady(0) {
		t.Fatal("closed ready signal should not count without the current ready state")
	}

	app.mu.Lock()
	app.trayReady = true
	app.mu.Unlock()
	if !app.waitForTrayReady(0) {
		t.Fatal("ready state should be accepted after the tray is marked ready")
	}

	app.mu.Lock()
	app.trayReady = false
	app.mu.Unlock()
	if app.waitForTrayReady(0) {
		t.Fatal("stale ready signal should not count after the tray exits")
	}
}

func TestBackgroundCloseWaitsForTrayReadySignal(t *testing.T) {
	app := NewApp()
	tray := newDesktopTray()
	app.mu.Lock()
	app.tray = tray
	app.mu.Unlock()

	go func() {
		time.Sleep(10 * time.Millisecond)
		app.mu.Lock()
		app.trayReady = true
		app.mu.Unlock()
		tray.markReady()
	}()

	if !app.waitForTrayReady(200 * time.Millisecond) {
		t.Fatal("waitForTrayReady should observe the tray becoming ready")
	}
}

func TestBackgroundRestoreMaximiseStrategy(t *testing.T) {
	tests := []struct {
		goos      string
		maximised bool
		want      bool
	}{
		{goos: "windows", maximised: true, want: true},
		{goos: "linux", maximised: true, want: true},
		{goos: "darwin", maximised: true, want: false},
		{goos: "windows", maximised: false, want: false},
	}
	for _, tt := range tests {
		if got := backgroundRestoreShouldMaximise(tt.goos, tt.maximised); got != tt.want {
			t.Fatalf("backgroundRestoreShouldMaximise(%q, %v) = %v, want %v", tt.goos, tt.maximised, got, tt.want)
		}
	}
}

func TestBackgroundRestorePlanAvoidsNormalWindowFlash(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		maximised bool
		want      backgroundRestorePlan
	}{
		{
			name:      "maximised Windows window",
			goos:      "windows",
			maximised: true,
			want:      backgroundRestorePlan{maximiseBeforeShow: true},
		},
		{
			name:      "normal Windows window",
			goos:      "windows",
			maximised: false,
			want:      backgroundRestorePlan{unminimiseAfterShow: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := backgroundRestorePlanFor(tt.goos, tt.maximised)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("backgroundRestorePlanFor(%q, %v) = %v, want %v", tt.goos, tt.maximised, got, tt.want)
			}
		})
	}
}

func TestEmitReadyInvokesReadyHook(t *testing.T) {
	app := NewApp()
	var calls atomic.Int32
	app.readyHook = func() {
		calls.Add(1)
	}

	app.emitReady(context.TODO())

	if got := calls.Load(); got != 1 {
		t.Fatalf("ready hook calls = %d, want 1", got)
	}
}

func TestAddOfficialProviderAccessPreservesBackgroundJobsWhenSavingKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	os.Unsetenv("DEEPSEEK_API_KEY")

	app := NewApp()
	app.readyHook = func() {}
	app.setTestCtrl(newBackgroundJobController(t, "provider-access-job"), "deepseek-flash/deepseek-flash")

	_, err := app.AddOfficialProviderAccess("deepseek", "sk-test")
	if err != nil || !controllerHasActiveRuntimeWork(app.activeCtrl()) {
		t.Fatalf("AddOfficialProviderAccess interrupted background work: %v", err)
	}
	p, _ := config.LoadForEdit(config.UserConfigPath()).Provider("deepseek")
	if !config.CredentialStored(p.APIKeyEnv) || p.APIKeyEnv == "DEEPSEEK_API_KEY" {
		t.Fatal("official key was not committed to a new reference")
	}
}

func TestDeleteProviderPreservesAffectedBackgroundJobs(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"model-a1"}, Default: "model-a1", Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b1"}, Default: "model-b1", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "provider-job.jsonl")
	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	defer ctrl.Close()
	app := NewApp()
	app.setTestCtrl(ctrl, "prov-a/model-a1")
	jm.StartForSession(agent.BranchID(path), "bash", "provider job", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	err := app.DeleteProvider("prov-a")
	if err != nil || !controllerHasActiveRuntimeWork(ctrl) {
		t.Fatalf("DeleteProvider interrupted background work: %v", err)
	}
	if _, ok := config.LoadForEdit(config.UserConfigPath()).Provider("prov-a"); ok {
		t.Fatal("provider deletion was not persisted")
	}
}

func TestDeleteProviderPreservesUnaffectedBackgroundJobsWhenSavingConfig(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-b/model-b1"
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"model-a1"}, Default: "model-a1", Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b1"}, Default: "model-b1", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(newBackgroundJobController(t, "provider-unaffected-job"), "prov-b/model-b1")

	err := app.DeleteProvider("prov-a")
	if err != nil || !controllerHasActiveRuntimeWork(app.activeCtrl()) {
		t.Fatalf("DeleteProvider interrupted unrelated background work: %v", err)
	}
	if _, ok := config.LoadForEdit(config.UserConfigPath()).Provider("prov-a"); ok {
		t.Fatal("provider deletion was not persisted")
	}
}

func TestRemoveBuiltInProviderAccessPreservesBackgroundJobsWhenSavingConfig(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "mimo-pro/mimo-v2.5-pro"

[desktop]
provider_access = ["deepseek-flash", "mimo-pro"]

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash", "deepseek-v4-pro"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"

[[providers]]
name = "mimo-pro"
kind = "openai"
base_url = "https://token-plan-cn.xiaomimimo.com/v1"
models = ["mimo-v2.5-pro"]
api_key_env = "MIMO_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(newBackgroundJobController(t, "provider-access-unaffected-job"), "mimo-token-plan/mimo-v2.5-pro")

	err := app.RemoveProviderAccess("deepseek-flash")
	if err != nil || !controllerHasActiveRuntimeWork(app.activeCtrl()) {
		t.Fatalf("RemoveProviderAccess interrupted background work: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	access := providerAccessSet(cfg.Desktop.ProviderAccess)
	if access["deepseek"] || access["deepseek-flash"] {
		t.Fatalf("provider access removal was not persisted: %+v", cfg.Desktop.ProviderAccess)
	}
}

func TestConnectKeySavesWhileBackgroundJobKeepsItsController(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	os.Unsetenv("DEEPSEEK_API_KEY")
	oldFetch := connectKeyBalanceFetch
	connectKeyBalanceFetch = func(context.Context, *http.Client, string, string) (*billing.Balance, error) {
		return &billing.Balance{Available: true}, nil
	}
	t.Cleanup(func() { connectKeyBalanceFetch = oldFetch })

	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(newBackgroundJobController(t, "connect-key-job"), "deepseek-flash/deepseek-flash")
	oldCtrl := app.activeCtrl()

	_, err := app.ConnectKey("sk-test")
	if err != nil {
		t.Fatalf("ConnectKey with background job: %v", err)
	}
	p, ok := config.LoadForEdit(config.UserConfigPath()).Provider("deepseek")
	if !ok || !p.Configured() || app.activeCtrl() != oldCtrl {
		t.Fatal("key save must configure the connection and preserve the background runtime")
	}
}

func TestSettingsRebuildRejectsBackgroundJobs(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "settings-job.jsonl")
	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	defer ctrl.Close()
	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(ctrl, "deepseek-flash/deepseek-flash")

	jm.StartForSession(agent.BranchID(path), "bash", "settings job", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	err := app.SetSandbox("enforce", true, "", nil, "")
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("SetSandbox with background job error = %v, want background-job guard", err)
	}
}

func TestTrashTopicRejectsBackgroundJob(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_stuck_trash"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Stuck trash"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSession(t, dir, "stuck-topic.jsonl", topicID, "Stuck trash", projectRoot)

	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: sessionPath, Label: "test", Jobs: jm, WorkspaceRoot: projectRoot})
	releaseJob := startNonCooperativeSessionJob(t, jm, sessionPath)
	defer func() {
		releaseJob()
		ctrl.Close()
	}()

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"stuck": {
				ID:            "stuck",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       topicID,
				TopicTitle:    "Stuck trash",
				Ctrl:          ctrl,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
			"keep": {
				ID:            "keep",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       "topic_keep",
				TopicTitle:    "Keep",
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
		},
		tabOrder:    []string{"stuck", "keep"},
		activeTabID: "stuck",
	}

	if err := app.TrashTopic(topicID); !errors.Is(err, errTopicHasActiveWork) {
		t.Fatalf("TrashTopic(background job) error = %v, want %v", err, errTopicHasActiveWork)
	}
	if _, ok := app.tabs["stuck"]; !ok {
		t.Fatal("rejected archive should keep the background-job topic tab")
	}
	if agent.IsCleanupPending(sessionPath) {
		t.Fatal("rejected archive should not mark session cleanup pending")
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("rejected archive should preserve the live session: %v", err)
	}
	trashPath := filepath.Join(dir, ".trash", "stuck-topic.jsonl", "stuck-topic.jsonl")
	if _, err := os.Stat(trashPath); !os.IsNotExist(err) {
		t.Fatalf("rejected archive created a trash entry, stat err = %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "Stuck trash" {
		t.Fatalf("rejected archive topic title = %q, want Stuck trash", got)
	}
}

func newBackgroundJobController(t *testing.T, label string) *control.Controller {
	t.Helper()
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, label+".jsonl")
	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	t.Cleanup(ctrl.Close)
	jm.StartForSession(agent.BranchID(path), "bash", label, func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	return ctrl
}
