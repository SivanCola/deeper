package main

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aead.dev/minisign"

	"deepzora/desktop/internal/update"
	"deepzora/internal/releaseassets"
)

func TestGenManifest(t *testing.T) {
	for _, version := range []string{"v0.0.1", "v0.0.2", "v1.2.0", "v1.3.0-rc.1"} {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			for _, spec := range releaseassets.DesktopAssets() {
				name := releaseassets.DesktopName(version, spec)
				if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "unrelated.deb"), []byte("ignored"), 0600); err != nil {
				t.Fatal(err)
			}
			notes := strings.Split(version, "-rc")[0]
			if err := genManifest(dir, version, "desktop-"+version, notes); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "latest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var m update.Manifest
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			if m.ArtifactNaming != "versioned-v1" || len(m.Downloads) != 11 || len(m.Platforms) != 5 || len(m.NativePackages) != 1 {
				t.Fatalf("invalid matrix: %+v", m)
			}
			if m.ReleaseNotesURL != "https://github.com/SivanCola/deepzora/releases/tag/"+notes+"/" {
				t.Fatal(m.ReleaseNotesURL)
			}
			for _, spec := range releaseassets.DesktopAssets() {
				name := releaseassets.DesktopName(version, spec)
				asset, ok := m.Downloads[name]
				if !ok || asset.URL != "https://github.com/SivanCola/deepzora/releases/download/desktop-"+version+"/"+name || asset.Sig != asset.URL+".minisig" || asset.Size == 0 || asset.SHA256 == "" || asset.InstallLayout != update.ElectronInstallLayout {
					t.Fatalf("invalid artifact: %+v", asset)
				}
				if _, ok := m.Downloads[spec.BuildName]; ok {
					t.Fatal("published build-name alias")
				}
				if spec.Channel == "platforms" && m.Platforms[spec.Platform] != asset {
					t.Fatal("wrong updater route")
				}
				if spec.Channel == "native_packages" && m.NativePackages[spec.Platform] != asset {
					t.Fatal("wrong native package route")
				}
			}
			if err := os.Remove(filepath.Join(dir, releaseassets.DesktopName(version, releaseassets.DesktopAssets()[0]))); err != nil {
				t.Fatal(err)
			}
			if err := genManifest(dir, version, "desktop-"+version); err == nil {
				t.Fatal("accepted incomplete artifact matrix")
			}
		})
	}
}

// TestSignFiles signs a file with a throwaway key pair (injected via env, exactly
// as CI passes the real key) and verifies the produced .minisig validates under the
// matching public key.
func TestSignFiles(t *testing.T) {
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := minisign.EncryptKey("pw", priv)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MINISIGN_PRIVATE_KEY", string(enc))
	t.Setenv("MINISIGN_PASSWORD", "pw")
	t.Setenv("MINISIGN_PUBLIC_KEY", pub.String())

	dir := t.TempDir()
	artifact := filepath.Join(dir, "Deepzora-linux-amd64.tar.gz")
	payload := []byte("pretend this is a release tarball")
	if err := os.WriteFile(artifact, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := signFiles([]string{artifact}); err != nil {
		t.Fatalf("signFiles: %v", err)
	}
	sig, err := os.ReadFile(artifact + ".minisig")
	if err != nil {
		t.Fatalf("read signature: %v", err)
	}
	if !minisign.Verify(pub, payload, sig) {
		t.Fatal("produced signature does not verify under the signing key")
	}
	other, _, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MINISIGN_PUBLIC_KEY", other.String())
	rejected := filepath.Join(dir, "mismatched-key.tar.gz")
	if err := os.WriteFile(rejected, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := signFiles([]string{rejected}); err == nil || !strings.Contains(err.Error(), "configured public key does not match") {
		t.Fatalf("mismatched public key was not rejected: %v", err)
	}
	if _, err := os.Stat(rejected + ".minisig"); !os.IsNotExist(err) {
		t.Fatalf("signature published with a mismatched key: %v", err)
	}
}

func TestGenWindowsPayloadManifestHashesExactReleaseUnit(t *testing.T) {
	dir := t.TempDir()
	for _, name := range update.WindowsPayloadFileNames() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("payload:"+name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := genWindowsPayloadManifest(dir, "v2.3.4"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, update.WindowsPayloadManifestName))
	if err != nil {
		t.Fatal(err)
	}
	hashes, err := update.DecodeWindowsPayloadManifest(b, "v2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range update.WindowsPayloadFileNames() {
		want := update.WindowsPayloadSHA256([]byte("payload:" + name))
		if hashes[name] != want {
			t.Fatalf("manifest hash for %s = %q, want %q", name, hashes[name], want)
		}
	}
}
