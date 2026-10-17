package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAttachmentUploadCleanupKeepsReplacementDirectoryUntouched(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "symlink"}[symlink], func(t *testing.T) {
			a, root, token := uploadFixture(t)
			var original, victim string
			a.attachmentIOHook = func() {
				original, victim = swapDesktopAttachmentDirectory(t, root, symlink)
				a.tabs["a"].SessionGeneration++
			}
			recorder := httptest.NewRecorder()
			a.attachmentUploadMiddleware()(http.NotFoundHandler()).ServeHTTP(recorder, uploadRequest(token, strings.NewReader("original upload")))
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
			}
			assertDesktopAttachmentCleanup(t, original, victim)
		})
	}
}

func TestDroppedImagePreviewFailureCleansPinnedDirectory(t *testing.T) {
	a, root, token := uploadFixture(t)
	source := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(source, mustDecodeBase64(t, desktopTinyPNG), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var original, victim string
	a.attachmentIOHook = func() {
		calls++
		if calls == 2 {
			original, victim = swapDesktopAttachmentDirectory(t, root, true)
			a.tabs["a"].SessionGeneration++
		}
	}
	if _, err := a.AttachDroppedForTarget(token, source); err == nil {
		t.Fatal("image with stale preview accepted")
	}
	if calls != 2 {
		t.Fatalf("hooks=%d", calls)
	}
	assertDesktopAttachmentCleanup(t, original, victim)
}

func swapDesktopAttachmentDirectory(t *testing.T, workspace string, symlink bool) (string, string) {
	t.Helper()
	directory := filepath.Join(workspace, ".deepzora", "attachments")
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("attachment files=%v err=%v", entries, err)
	}
	replacement := t.TempDir()
	victim := filepath.Join(replacement, entries[0].Name())
	if err := os.WriteFile(victim, []byte("keep replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if symlink {
		probe := filepath.Join(workspace, "symlink-probe")
		if err := os.Symlink(replacement, probe); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if err := os.Remove(probe); err != nil {
			t.Fatal(err)
		}
	}
	original := directory + "-original"
	if err := os.Rename(directory, original); err != nil {
		if runtime.GOOS == "windows" && os.IsPermission(err) {
			t.Log("Windows denied replacement of the pinned attachment directory")
			return directory, victim
		}
		t.Fatal(err)
	}
	if symlink {
		err = os.Symlink(replacement, directory)
	} else {
		err = os.Rename(replacement, directory)
		victim = filepath.Join(directory, entries[0].Name())
	}
	if err != nil {
		t.Fatal(err)
	}
	return original, victim
}

func assertDesktopAttachmentCleanup(t *testing.T, original, victim string) {
	t.Helper()
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep replacement" {
		t.Fatalf("replacement=%q err=%v", data, err)
	}
	if entries, err := os.ReadDir(original); err != nil || len(entries) != 0 {
		t.Fatalf("original partial files=%v err=%v", entries, err)
	}
}
