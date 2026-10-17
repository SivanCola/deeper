package control

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// SaveAttachmentStreamInRoot stores ordinary file bytes without the legacy
// data-URL size cap. A 64 KiB buffer bounds memory, not total stored bytes.
func SaveAttachmentStreamInRoot(ctx context.Context, root, name string, source io.Reader, commit ...AttachmentCommit) (path string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(name))
	if !safeAttachmentExt.MatchString(ext) {
		ext = ".bin"
	}
	rel, f, err := createAttachmentFileIn(abs, ext)
	if err != nil {
		return "", err
	}
	defer f.release()
	defer func() {
		if err != nil {
			f.remove()
		}
	}()
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := source.Read(buf)
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if written, err := f.Write(buf[:n]); err != nil {
				return "", err
			} else if written != n {
				return "", io.ErrShortWrite
			}
			total += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if total == 0 {
		return "", fmt.Errorf("attachment is empty")
	}
	if err := f.finish(abs, rel, commit); err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
