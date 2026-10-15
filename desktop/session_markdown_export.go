package main

import (
	"context"
	"deepzora/internal/sessionexport"
	"io"
	"os"
	"path/filepath"
	"strings"

	"deepzora/internal/fileutil"
)

// SaveSessionMarkdownForTab writes the complete host-side transcript without
// transferring or joining it in the renderer. The destination is published
// only after the buffered stream has been flushed and synced.
func (a *App) SaveSessionMarkdownForTab(tabID, path, title string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	query, ref, err := a.canonicalSessionQuery(tabID)
	if err != nil {
		return err
	}
	snapshot, err := query.CaptureExportSnapshot(context.Background(), ref)
	if err != nil {
		return err
	}
	snapshot.Title = title
	dir, err := os.MkdirTemp("", "deepzora-markdown-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if _, err = sessionexport.Build(context.Background(), query, snapshot, dir, nil); err != nil {
		return err
	}
	return writeStreamingExport(path, func(dst io.Writer) error {
		file, err := os.Open(filepath.Join(dir, "markdown"))
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(dst, file)
		return err
	})
}

func writeStreamingExport(path string, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".deepzora-session-export-*")
	if err != nil {
		return exportOperationError("save session export", path, err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return exportOperationError("save session export", path, err)
	}
	if err := write(tmp); err != nil {
		return exportOperationError("save session export", path, err)
	}
	if err := tmp.Sync(); err != nil {
		return exportOperationError("save session export", path, err)
	}
	if err := tmp.Close(); err != nil {
		return exportOperationError("save session export", path, err)
	}
	if err := fileutil.ReplaceFile(tmpPath, path); err != nil {
		return exportOperationError("save session export", path, err)
	}
	keep = true
	return nil
}
