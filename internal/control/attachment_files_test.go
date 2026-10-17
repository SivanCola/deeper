package control

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func replaceAttachmentDirectory(t *testing.T, workspace string, symlink bool) (string, string) {
	t.Helper()
	directory := filepath.Join(workspace, ".deepzora", "attachments")
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("attachment files=%v err=%v", entries, err)
	}
	name := entries[0].Name()
	replacement := t.TempDir()
	victim := filepath.Join(replacement, name)
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
		t.Fatal(err)
	}
	if symlink {
		err = os.Symlink(replacement, directory)
	} else {
		err = os.Rename(replacement, directory)
		victim = filepath.Join(directory, name)
	}
	if err != nil {
		t.Fatal(err)
	}
	return original, victim
}

func assertAttachmentReplacementPreserved(t *testing.T, original, victim string) {
	t.Helper()
	if got, err := os.ReadFile(victim); err != nil || string(got) != "keep replacement" {
		t.Fatalf("replacement file=%q err=%v", got, err)
	}
	if entries, err := os.ReadDir(original); err != nil || len(entries) != 0 {
		t.Fatalf("original partial files=%v err=%v", entries, err)
	}
}

func TestAttachmentCommitCleansOriginalDirectoryAfterReplacement(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "symlink"}[symlink], func(t *testing.T) {
			for _, reject := range []bool{false, true} {
				t.Run(map[bool]string{false: "successful callback", true: "rejected callback"}[reject], func(t *testing.T) {
					workspace := t.TempDir()
					var original, victim string
					rejected := errors.New("owner changed")
					_, err := SaveAttachmentBytesInRoot(workspace, "notes.txt", []byte("original upload"), func(string) error {
						original, victim = replaceAttachmentDirectory(t, workspace, symlink)
						if reject {
							return rejected
						}
						return nil
					})
					if err == nil || (reject && !errors.Is(err, rejected)) {
						t.Fatalf("save error=%v", err)
					}
					assertAttachmentReplacementPreserved(t, original, victim)
				})
			}
		})
	}
}

type attachmentReadFunc func([]byte) (int, error)

func (read attachmentReadFunc) Read(p []byte) (int, error) { return read(p) }

func TestAttachmentStreamCleansPinnedDirectoryAfterReadReplacement(t *testing.T) {
	for _, readErr := range []error{io.EOF, io.ErrUnexpectedEOF} {
		t.Run(readErr.Error(), func(t *testing.T) {
			workspace := t.TempDir()
			var original, victim string
			reader := attachmentReadFunc(func(p []byte) (int, error) {
				original, victim = replaceAttachmentDirectory(t, workspace, true)
				return copy(p, "partial stream"), readErr
			})
			if _, err := SaveAttachmentStreamInRoot(context.Background(), workspace, "upload.apk", reader); err == nil {
				t.Fatal("directory replacement accepted")
			}
			assertAttachmentReplacementPreserved(t, original, victim)
		})
	}
}

func TestAttachmentCommitDoesNotDeleteReplacedFile(t *testing.T) {
	workspace := t.TempDir()
	var replacement, original string
	_, err := SaveAttachmentBytesInRoot(workspace, "notes.txt", []byte("upload"), func(rel string) error {
		replacement = filepath.Join(workspace, filepath.FromSlash(rel))
		original = replacement + ".original"
		if err := os.Rename(replacement, original); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replacement, []byte("keep replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
		return errors.New("owner changed")
	})
	if err == nil {
		t.Fatal("replaced file accepted")
	}
	if got, err := os.ReadFile(replacement); err != nil || string(got) != "keep replacement" {
		t.Fatalf("replacement=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(original); err != nil || string(got) != "upload" {
		t.Fatalf("original=%q err=%v", got, err)
	}
}

func TestAttachmentStreamRejectsManagedDirectorySymlinks(t *testing.T) {
	for _, linkManaged := range []bool{false, true} {
		t.Run(map[bool]string{false: "attachments", true: "deepzora"}[linkManaged], func(t *testing.T) {
			workspace, outside := t.TempDir(), t.TempDir()
			link := filepath.Join(workspace, ".deepzora")
			if !linkManaged {
				if err := os.Mkdir(link, 0o755); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(link, "attachments")
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Skipf("symlink unsupported: %v", err)
			}
			if _, err := SaveAttachmentStreamInRoot(context.Background(), workspace, "notes.txt", strings.NewReader("upload")); err == nil {
				t.Fatal("managed symlink accepted")
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Fatalf("outside files=%v err=%v", entries, err)
			}
		})
	}
}
