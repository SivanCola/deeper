package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AttachmentCommit validates a completed write before its pinned directory is
// released. A failed check removes the file through the original directory.
type AttachmentCommit func(path string) error

type attachmentFile struct {
	*os.File
	directory *os.Root
	name      string
	identity  os.FileInfo
}

func (f *attachmentFile) release() {
	_ = f.Close()
	_ = f.directory.Close()
}

func (f *attachmentFile) remove() {
	_ = f.Close()
	current, err := f.directory.Lstat(f.name)
	if err == nil && os.SameFile(f.identity, current) {
		_ = f.directory.Remove(f.name)
	}
}

func openAttachmentDirectoryIn(base string, create bool) (*os.Root, error) {
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	workspace, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer workspace.Close()
	managed, err := openAttachmentSubdirectory(workspace, ".deepzora", create)
	if err != nil {
		return nil, err
	}
	defer managed.Close()
	return openAttachmentSubdirectory(managed, "attachments", create)
}

func openAttachmentSubdirectory(parent *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		if err := parent.Mkdir(name, 0o755); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("attachment path must not contain symlinks")
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("attachment path exists but is not a directory")
	}
	directory, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := directory.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = directory.Close()
		return nil, fmt.Errorf("attachment directory changed while opening")
	}
	return directory, nil
}

func createAttachmentFile(ext string) (string, *attachmentFile, error) {
	return createAttachmentFileIn(".", ext)
}

func createAttachmentFileIn(base, ext string) (string, *attachmentFile, error) {
	if !safeAttachmentExt.MatchString(ext) {
		ext = ".bin"
	}
	directory, err := openAttachmentDirectoryIn(base, true)
	if err != nil {
		return "", nil, err
	}
	for range maxAttachmentCreateAttempts {
		rel := attachmentPath(ext)
		name := filepath.Base(rel)
		file, err := directory.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			_ = directory.Close()
			return "", nil, err
		}
		identity, err := file.Stat()
		if err != nil {
			_ = file.Close()
			_ = directory.Remove(name)
			_ = directory.Close()
			return "", nil, err
		}
		// Keep this directory pinned through cleanup, even if its path is replaced.
		return rel, &attachmentFile{File: file, directory: directory, name: name, identity: identity}, nil
	}
	_ = directory.Close()
	return "", nil, fmt.Errorf("create unique attachment path")
}

func (f *attachmentFile) finish(root, path string, commit []AttachmentCommit) error {
	if err := f.validate(root); err != nil {
		return err
	}
	for _, check := range commit {
		if check != nil {
			if err := check(filepath.ToSlash(path)); err != nil {
				return err
			}
		}
	}
	if err := f.validate(root); err != nil {
		return err
	}
	return f.Close()
}

func (f *attachmentFile) validate(root string) error {
	current, err := openAttachmentDirectoryIn(root, false)
	if err != nil {
		return err
	}
	defer current.Close()
	original, err := f.directory.Stat(".")
	if err != nil {
		return err
	}
	now, err := current.Stat(".")
	if err != nil || !os.SameFile(original, now) {
		return fmt.Errorf("attachment directory changed while saving")
	}
	written, err := f.Stat()
	if err != nil {
		return err
	}
	named, err := current.Lstat(f.name)
	if err != nil || !os.SameFile(written, named) {
		return fmt.Errorf("attachment changed while saving")
	}
	return nil
}
