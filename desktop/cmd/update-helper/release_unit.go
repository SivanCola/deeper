package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"deepzora/internal/installlayout"
)

// stagedWindowsPayloadMembers binds each named staged file to its signed
// digest before the activator copies it into the version tree.
func stagedWindowsPayloadMembers(stagingDir string, hashes map[string]string, names []string) ([]installlayout.Member, error) {
	members := make([]installlayout.Member, 0, len(names))
	for _, name := range names {
		src := filepath.Join(stagingDir, filepath.FromSlash(name))
		if err := verifyStagedWindowsPayloadFile(src, hashes[name]); err != nil {
			return nil, fmt.Errorf("staged %s: %w", name, err)
		}
		members = append(members, installlayout.Member{Name: name, Path: src, Mode: 0o700})
	}
	return members, nil
}

func verifyStagedWindowsPayloadFile(path, wantSHA256 string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), strings.TrimSpace(wantSHA256)) {
		return fmt.Errorf("does not match the signed release manifest")
	}
	return nil
}
