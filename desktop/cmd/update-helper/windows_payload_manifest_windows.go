//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"deepzora/desktop/internal/update"
)

// maxWindowsPayloadMetadataSize bounds the signed manifest read; a schema 2
// manifest lists every shell tree file, so it is well above the flat size.
const maxWindowsPayloadMetadataSize = 1 << 20

var verifyWindowsPayloadManifestFn = update.Verify

func loadWindowsPayloadManifest(stagingDir, expectedVersion string) (map[string]string, error) {
	manifest, err := readWindowsPayloadMetadata(filepath.Join(stagingDir, update.WindowsPayloadManifestName))
	if err != nil {
		return nil, fmt.Errorf("read signed release manifest: %w", err)
	}
	signature, err := readWindowsPayloadMetadata(filepath.Join(stagingDir, update.WindowsPayloadSignatureName))
	if err != nil {
		return nil, fmt.Errorf("read signed release manifest signature: %w", err)
	}
	if err := verifyWindowsPayloadManifestFn(manifest, signature); err != nil {
		return nil, fmt.Errorf("verify signed release manifest: %w", err)
	}
	hashes, err := update.DecodeWindowsPayloadManifest(manifest, expectedVersion)
	if err != nil {
		return nil, err
	}
	return hashes, nil
}

func readWindowsPayloadMetadata(path string) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxWindowsPayloadMetadataSize {
		return nil, fmt.Errorf("%s is not a bounded regular file", filepath.Base(path))
	}
	if !os.SameFile(pathInfo, info) {
		return nil, fmt.Errorf("%s changed before it was opened", filepath.Base(path))
	}
	data, err := io.ReadAll(io.LimitReader(file, maxWindowsPayloadMetadataSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxWindowsPayloadMetadataSize {
		return nil, fmt.Errorf("%s changed size while it was read", filepath.Base(path))
	}
	return data, nil
}
