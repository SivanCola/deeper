package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"deepzora/desktop/internal/update"
)

func writeWindowsPayloadManifestForTest(t *testing.T, staging, version string) {
	t.Helper()
	hashes := make(map[string]string)
	for _, name := range update.WindowsPayloadFileNames() {
		content, err := os.ReadFile(filepath.Join(staging, name))
		if os.IsNotExist(err) {
			content = []byte("missing:" + name)
		} else if err != nil {
			t.Fatal(err)
		}
		hashes[name] = update.WindowsPayloadSHA256(content)
	}
	treeRoot := filepath.Join(staging, "app")
	if _, err := os.Lstat(treeRoot); err == nil {
		err := filepath.WalkDir(treeRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(staging, path)
			if err != nil {
				return err
			}
			hashes[filepath.ToSlash(rel)] = update.WindowsPayloadSHA256(content)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := update.EncodeWindowsPayloadManifest(version, hashes)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, update.WindowsPayloadManifestName), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, update.WindowsPayloadSignatureName), []byte("test signature"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func acceptWindowsPayloadManifestForTest(t *testing.T) {
	t.Helper()
	original := verifyWindowsPayloadManifestFn
	verifyWindowsPayloadManifestFn = func(_, signature []byte) error {
		if string(signature) != "test signature" {
			return errors.New("unexpected test signature")
		}
		return nil
	}
	t.Cleanup(func() { verifyWindowsPayloadManifestFn = original })
}

func useTestWindowsPayloadManifest(t *testing.T, staging, version string) {
	t.Helper()
	writeWindowsPayloadManifestForTest(t, staging, version)
	acceptWindowsPayloadManifestForTest(t)
	useUnverifiedPayloadReaderForTest(t)
}

func useUnverifiedPayloadReaderForTest(t *testing.T) {
	t.Helper()
	original := readVerifiedWindowsStagedPayloadFn
	readVerifiedWindowsStagedPayloadFn = os.ReadFile
	t.Cleanup(func() { readVerifiedWindowsStagedPayloadFn = original })
}
