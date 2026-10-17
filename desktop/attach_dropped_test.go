package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"deepzora/internal/control"
)

const desktopTinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

func TestWorkspaceRelativeIn(t *testing.T) {
	root := t.TempDir()

	if rel, ok := workspaceRelativeIn(filepath.Join(root, "sub", "file.go"), root); !ok || rel != "sub/file.go" {
		t.Fatalf("in-tree = (%q, %v), want (sub/file.go, true)", rel, ok)
	}
	if _, ok := workspaceRelativeIn(filepath.Join(filepath.Dir(root), "sibling.txt"), root); ok {
		t.Fatal("a path above the workspace must not resolve as in-tree")
	}
}

func TestIsImageExt(t *testing.T) {
	for _, p := range []string{"a.png", "A.PNG", "b.jpeg", "c.webp"} {
		if !isImageExt(p) {
			t.Errorf("%q should be an image extension", p)
		}
	}
	for _, p := range []string{"notes.pdf", "main.go", "noext"} {
		if isImageExt(p) {
			t.Errorf("%q should not be an image extension", p)
		}
	}
}

func TestSavePastedImageUsesActiveWorkspaceRoot(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	launchRoot := t.TempDir()
	projectRoot := t.TempDir()
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatal(err)
	}
	projectRoot, _ = os.Getwd()
	if err := os.Chdir(launchRoot); err != nil {
		t.Fatal(err)
	}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"project": {ID: "project", WorkspaceRoot: projectRoot},
		},
		activeTabID: "project",
	}

	got, err := app.SavePastedImage("data:image/png;base64," + desktopTinyPNG)
	if err != nil {
		t.Fatalf("SavePastedImage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(got))); err != nil {
		t.Fatalf("pasted image should be saved under active workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(launchRoot, filepath.FromSlash(got))); !os.IsNotExist(err) {
		t.Fatalf("pasted image should not be saved under launch root, stat err=%v", err)
	}
	preview, err := app.AttachmentDataURL(got)
	if err != nil {
		t.Fatalf("AttachmentDataURL: %v", err)
	}
	if !strings.HasPrefix(preview, "data:image/png;base64,") {
		t.Fatalf("preview = %q, want png data URL", preview)
	}
}

func TestReadSessionAttachmentForTabRequiresCanonicalSession(t *testing.T) {
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"a": {ID: "a", WorkspaceRoot: t.TempDir(), SessionGeneration: 1},
		},
		activeTabID: "a",
	}
	if _, err := app.ReadSessionAttachmentForTab("a", strings.Repeat("ab", 32), 0); err == nil {
		t.Fatal("ReadSessionAttachmentForTab accepted a tab without a canonical session")
	}
}

func TestStageImageForTabRequiresBoundController(t *testing.T) {
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"a": {ID: "a", WorkspaceRoot: t.TempDir()},
		},
		activeTabID: "a",
	}
	if _, err := app.StageImageForTab("a", "op-1", "shot.png", "image/png", "data:image/png;base64,"+desktopTinyPNG); err == nil {
		t.Fatal("StageImageForTab accepted a tab without a session controller")
	}
}

func TestLegacyAttachmentRPCRejectsMissingTarget(t *testing.T) {
	t.Chdir(t.TempDir())
	app := &App{tabs: map[string]*WorkspaceTab{}}
	if _, err := app.SavePastedImage("data:image/png;base64," + desktopTinyPNG); err == nil {
		t.Fatal("legacy attachment RPC fell back to the process working directory")
	}
	if _, err := os.Stat(filepath.Join(".deepzora", "attachments")); !os.IsNotExist(err) {
		t.Fatalf("missing target created a process-relative attachment directory: %v", err)
	}
}

func TestGlobalAttachmentRPCUsesStableGlobalWorkspace(t *testing.T) {
	isolateDesktopUserDirs(t)
	if _, err := os.Stat(globalWorkspaceRoot()); !os.IsNotExist(err) {
		t.Fatalf("fresh global workspace already exists: %v", err)
	}
	launchRoot := t.TempDir()
	t.Chdir(launchRoot)
	app := &App{
		tabs:        map[string]*WorkspaceTab{"global": {ID: "global", Scope: "global"}},
		activeTabID: "global",
	}
	rel, err := app.SavePastedImage("data:image/png;base64," + desktopTinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(globalWorkspaceRoot(), filepath.FromSlash(rel))); err != nil {
		t.Fatalf("global attachment missing from stable global workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(launchRoot, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Fatalf("global attachment was written under process cwd: %v", err)
	}
}

func TestSavePastedImageForTabKeepsCapturedWorkspaceAcrossActiveSwitch(t *testing.T) {
	launchRoot := t.TempDir()
	projectA := t.TempDir()
	projectB := t.TempDir()
	t.Chdir(launchRoot)
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"a": {ID: "a", WorkspaceRoot: projectA},
			"b": {ID: "b", WorkspaceRoot: projectB},
		},
		activeTabID: "a",
	}
	app.attachmentIOHook = func() {
		app.mu.Lock()
		app.activeTabID = "b"
		app.mu.Unlock()
	}

	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := app.SavePastedImageForTab("a", "data:image/png;base64,"+desktopTinyPNG)
	if err != nil {
		t.Fatalf("SavePastedImageForTab: %v", err)
	}
	after, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("process cwd changed from %q to %q", before, after)
	}
	if _, err := os.Stat(filepath.Join(projectA, filepath.FromSlash(got))); err != nil {
		t.Fatalf("captured workspace attachment missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectB, filepath.FromSlash(got))); !os.IsNotExist(err) {
		t.Fatalf("attachment drifted to active workspace B: %v", err)
	}
}

func TestAttachmentDataURLForTabIsolatesSameRelativePath(t *testing.T) {
	projectA := t.TempDir()
	projectB := t.TempDir()
	rel := ".deepzora/attachments/shared.png"
	rawA, err := base64.StdEncoding.DecodeString(desktopTinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	rawB := append([]byte(nil), rawA...)
	rawB[len(rawB)-1] ^= 1
	for root, raw := range map[string][]byte{projectA: rawA, projectB: rawB} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{tabs: map[string]*WorkspaceTab{
		"a": {ID: "a", WorkspaceRoot: projectA},
		"b": {ID: "b", WorkspaceRoot: projectB},
	}}

	one, err := app.AttachmentDataURLForTab("a", rel)
	if err != nil {
		t.Fatal(err)
	}
	two, err := app.AttachmentDataURLForTab("b", rel)
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("same relative attachment path resolved to the same workspace bytes")
	}
}

func TestSavePastedImageForTabRejectsClosedTargetAndRemovesWrite(t *testing.T) {
	projectRoot := t.TempDir()
	tab := &WorkspaceTab{ID: "a", WorkspaceRoot: projectRoot}
	app := &App{tabs: map[string]*WorkspaceTab{"a": tab}, activeTabID: "a"}
	var once sync.Once
	app.attachmentIOHook = func() {
		once.Do(func() {
			app.mu.Lock()
			delete(app.tabs, "a")
			app.mu.Unlock()
		})
	}

	if _, err := app.SavePastedImageForTab("a", "data:image/png;base64,"+desktopTinyPNG); err == nil {
		t.Fatal("closed attachment target was accepted")
	}
	entries, err := os.ReadDir(filepath.Join(projectRoot, ".deepzora", "attachments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected attachment write left files: %v", entries)
	}
}

func TestAttachmentDataURLForTabRejectsReplacedRuntime(t *testing.T) {
	projectRoot := t.TempDir()
	rel, err := control.SaveImageBytesInRoot(projectRoot, "image/png", mustDecodeBase64(t, desktopTinyPNG))
	if err != nil {
		t.Fatal(err)
	}
	first := control.New(control.Options{WorkspaceRoot: projectRoot})
	second := control.New(control.Options{WorkspaceRoot: projectRoot})
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	tab := &WorkspaceTab{ID: "a", WorkspaceRoot: projectRoot, Ctrl: first}
	app := &App{tabs: map[string]*WorkspaceTab{"a": tab}, activeTabID: "a"}
	var once sync.Once
	app.attachmentIOHook = func() {
		once.Do(func() {
			app.mu.Lock()
			tab.Ctrl = second
			app.mu.Unlock()
		})
	}

	if _, err := app.AttachmentDataURLForTab("a", rel); err == nil {
		t.Fatal("preview from replaced runtime was accepted")
	}
}

func mustDecodeBase64(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSavePastedImageUsesCurrentSessionWorkspaceAfterNavigation(t *testing.T) {
	app, tab, target, targetRoot, _ := canonicalWorkspaceOpenFixture(t)
	previousRoot := tab.WorkspaceRoot
	launchRoot := t.TempDir()
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(launchRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := app.resumeCanonicalSessionForTranscript(tab, tab.Ctrl, sessionRoute(target.Ref().SessionID), 10, false); err != nil {
		t.Fatal(err)
	}
	got, err := app.SavePastedImage("data:image/png;base64," + desktopTinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, filepath.FromSlash(got))); err != nil {
		t.Fatalf("target workspace attachment: %v", err)
	}
	for _, root := range []string{previousRoot, launchRoot} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(got))); !os.IsNotExist(err) {
			t.Fatalf("attachment leaked to another workspace %q: %v", root, err)
		}
	}
}

func TestAttachDroppedUsesActiveWorkspaceRoot(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	launchRoot := t.TempDir()
	projectRoot := t.TempDir()
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatal(err)
	}
	projectRoot, _ = os.Getwd()
	if err := os.Chdir(launchRoot); err != nil {
		t.Fatal(err)
	}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"project": {ID: "project", WorkspaceRoot: projectRoot},
		},
		activeTabID: "project",
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(projectRoot, "sub", "notes.txt")
	if err := os.WriteFile(target, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := app.AttachDropped(target)
	if err != nil {
		t.Fatalf("AttachDropped: %v", err)
	}
	if got.Kind != "workspace" || got.Path != "sub/notes.txt" {
		t.Fatalf("got %+v, want workspace ref sub/notes.txt", got)
	}
}

func TestAttachDroppedImageUsesActiveWorkspaceRoot(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	launchRoot := t.TempDir()
	projectRoot := t.TempDir()
	if err := os.Chdir(launchRoot); err != nil {
		t.Fatal(err)
	}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"project": {ID: "project", WorkspaceRoot: projectRoot},
		},
		activeTabID: "project",
	}
	raw, err := base64.StdEncoding.DecodeString(desktopTinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(outside, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := app.AttachDropped(outside)
	if err != nil {
		t.Fatalf("AttachDropped: %v", err)
	}
	if got.Kind != "attachment" || !strings.HasSuffix(got.Path, ".png") {
		t.Fatalf("got %+v, want png attachment", got)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(got.Path))); err != nil {
		t.Fatalf("dropped image should be saved under active workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(launchRoot, filepath.FromSlash(got.Path))); !os.IsNotExist(err) {
		t.Fatalf("dropped image should not be saved under launch root, stat err=%v", err)
	}
	if !strings.HasPrefix(got.PreviewURL, "data:image/png;base64,") {
		t.Fatalf("preview = %q, want png data URL", got.PreviewURL)
	}
}

func TestAttachDroppedInWorkspaceReferencesInPlace(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if err := os.MkdirAll(filepath.Join(cwd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cwd, "sub", "notes.txt")
	if err := os.WriteFile(target, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{
		tabs:        map[string]*WorkspaceTab{"project": {ID: "project", WorkspaceRoot: cwd}},
		activeTabID: "project",
	}

	got, err := app.AttachDropped(target)
	if err != nil {
		t.Fatalf("AttachDropped: %v", err)
	}
	if got.Kind != "workspace" || got.Path != "sub/notes.txt" {
		t.Fatalf("got %+v, want workspace ref sub/notes.txt", got)
	}
}

func TestAttachDroppedOutsideWorkspaceReferencesFile(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	outside := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(outside, []byte("%PDF body"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	c := control.New(control.Options{WorkspaceRoot: root})
	t.Cleanup(c.Close)
	app := &App{
		tabs:        map[string]*WorkspaceTab{"project": {ID: "project", WorkspaceRoot: root, Ctrl: c}},
		activeTabID: "project",
	}

	got, err := app.AttachDropped(outside)
	if err != nil {
		t.Fatalf("AttachDropped: %v", err)
	}
	if got.Kind != "workspace" || !filepath.IsAbs(got.Path) || got.DisplayPath == "" {
		t.Fatalf("got %+v, want exact file reference", got)
	}
}

func TestAttachDroppedImageStoresThumbnail(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	if err := os.WriteFile(filepath.Join(cwd, "shot.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{
		tabs:        map[string]*WorkspaceTab{"project": {ID: "project", WorkspaceRoot: cwd}},
		activeTabID: "project",
	}

	got, err := app.AttachDropped(filepath.Join(cwd, "shot.png"))
	if err != nil {
		t.Fatalf("AttachDropped: %v", err)
	}
	if got.Kind != "attachment" || !strings.HasSuffix(got.Path, ".png") {
		t.Fatalf("got %+v, want png attachment", got)
	}
	if !strings.HasPrefix(got.PreviewURL, "data:image/png;base64,") {
		t.Fatalf("preview = %q, want png data URL", got.PreviewURL)
	}
}

func TestAttachDroppedOutsideWorkspaceDirRegistersWorkspaceRef(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "Folder With Spaces")
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "sub", "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectedOutside := outside
	if resolved, err := filepath.EvalSymlinks(outside); err == nil {
		expectedOutside = resolved
	}
	expectedDisplayPath := filepath.ToSlash(expectedOutside)
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}

	ctrl := control.New(control.Options{WorkspaceRoot: workspace})
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"project": {ID: "project", WorkspaceRoot: workspace, Ctrl: ctrl},
		},
		activeTabID: "project",
	}

	got, err := app.AttachDropped(outside)
	if err != nil {
		t.Fatalf("AttachDropped: %v", err)
	}
	if got.Kind != "workspace" || !got.IsDir {
		t.Fatalf("got %+v, want workspace directory ref", got)
	}
	if !strings.HasPrefix(got.Path, "__deepzora_external_folder/") || strings.ContainsAny(got.Path, " \t\r\n") {
		t.Fatalf("external folder path token = %q, want whitespace-free external token", got.Path)
	}
	if got.DisplayPath != expectedDisplayPath {
		t.Fatalf("display path = %q, want %q", got.DisplayPath, expectedDisplayPath)
	}

	block, errs := ctrl.ResolveScopedRefs(context.Background(), "inspect @"+got.Path+"/")
	if len(errs) != 0 {
		t.Fatalf("ResolveScopedRefs errors = %v", errs)
	}
	if !strings.Contains(block, `<dir path="`+expectedDisplayPath+`">`) ||
		!strings.Contains(block, "sub/") ||
		!strings.Contains(block, "sub/notes.txt") {
		t.Fatalf("external dropped folder should resolve as dir context:\n%s", block)
	}
}

func TestAttachDroppedOutsideWorkspaceDirRegistersAfterCurrentSessionNavigation(t *testing.T) {
	app, tab, target, targetRoot, _ := canonicalWorkspaceOpenFixture(t)
	oldCtrl := tab.Ctrl
	outside := filepath.Join(t.TempDir(), "External")
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "sub", "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.resumeCanonicalSessionForTranscript(tab, oldCtrl, sessionRoute(target.Ref().SessionID), 10, false); err != nil {
		t.Fatal(err)
	}
	got, err := app.AttachDropped(outside)
	if err != nil {
		t.Fatal(err)
	}
	if tab.Ctrl == oldCtrl {
		t.Fatal("navigation reused another session's controller")
	}
	if gotRoot := normalizeProjectRoot(tab.Ctrl.WorkspaceRoot()); gotRoot != normalizeProjectRoot(targetRoot) {
		t.Fatalf("controller workspace = %q, want %q", gotRoot, targetRoot)
	}
	resolver, ok := tab.Ctrl.(interface {
		ResolveScopedRefs(context.Context, string) (string, []string)
	})
	if !ok {
		t.Fatalf("controller does not resolve scoped refs: %T", tab.Ctrl)
	}
	block, errs := resolver.ResolveScopedRefs(context.Background(), "inspect @"+got.Path+"/")
	if len(errs) != 0 {
		t.Fatalf("ResolveScopedRefs errors = %v", errs)
	}
	if !strings.Contains(block, "sub/notes.txt") {
		t.Fatalf("external folder did not resolve on current controller:\n%s", block)
	}
}
