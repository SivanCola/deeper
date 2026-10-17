// Command sign is the CI-side signing and manifest tool for desktop releases. It
// is never shipped in any artifact — the release workflow invokes it via
// `go run ./cmd/sign`. It shares desktop/internal/update with the running updater
// so the sign path and the verify path use one definition of the manifest and one
// minisign implementation.
//
// Subcommands:
//
//	sign <file>...               Write <file>.minisig for each file, signing with the
//	                             encrypted minisign private key in $MINISIGN_PRIVATE_KEY
//	                             (decrypted with $MINISIGN_PASSWORD).
//
//	manifest <dir> <ver> <tag> [notes-ver]
//	                             Scan <dir> for the per-platform artifacts, compute
//	                             size + sha256, and write <dir>/latest.json with GitHub
//	                             release download URLs. The R2 mirror step rewrites those
//	                             URLs to the CDN afterwards (url + sig fields together).
//
//	windows-payload <dir> <ver> Write a deterministic manifest of the installer
//	                             executables and every file under <dir>/app.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"aead.dev/minisign"

	"deepzora/desktop/internal/update"
	"deepzora/internal/installlayout"
	"deepzora/internal/releaseassets"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "sign":
		err = signFiles(os.Args[2:])
	case "manifest":
		if len(os.Args) != 5 && len(os.Args) != 6 {
			usage()
		}
		notesVersion := os.Args[3]
		if len(os.Args) == 6 {
			notesVersion = os.Args[5]
		}
		err = genManifest(os.Args[2], os.Args[3], os.Args[4], notesVersion)
	case "windows-payload":
		if len(os.Args) != 4 {
			usage()
		}
		err = genWindowsPayloadManifest(os.Args[2], os.Args[3])
	case "genkey":
		if len(os.Args) != 3 {
			usage()
		}
		err = genKey(os.Args[2])
	case "verify":
		if len(os.Args) != 3 {
			usage()
		}
		err = verifyFile(os.Args[2])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:\n  sign <file>...\n  manifest <dir> <version> <tag> [notes-version]\n  windows-payload <dir> <version>\n  genkey <dir>\n  verify <file>")
	os.Exit(2)
}

func genWindowsPayloadManifest(dir, version string) error {
	hashes := make(map[string]string)
	for _, name := range update.WindowsPayloadFileNames() {
		sum, err := hashRegularFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("Windows payload %s: %w", name, err)
		}
		hashes[name] = sum
	}
	if err := hashWindowsPayloadTree(dir, hashes); err != nil {
		return err
	}
	b, err := update.EncodeWindowsPayloadManifest(version, hashes)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, update.WindowsPayloadManifestName), b, 0o644)
}

// hashWindowsPayloadTree records every regular file under dir/app with its
// forward-slash name; a symlink anywhere in the tree fails the build.
func hashWindowsPayloadTree(dir string, hashes map[string]string) error {
	root := filepath.Join(dir, installlayout.AppShellDirName)
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Windows payload %s: %w", installlayout.AppShellDirName, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("Windows payload %s is not a directory", installlayout.AppShellDirName)
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		name := filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("Windows payload %s: symlinks are not allowed", name)
		}
		if d.IsDir() {
			return nil
		}
		sum, err := hashRegularFile(path)
		if err != nil {
			return fmt.Errorf("Windows payload %s: %w", name, err)
		}
		hashes[name] = sum
		return nil
	})
}

func hashRegularFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	_, sum, err := hashFile(path)
	return sum, err
}

// verifyFile checks <file> against <file>.minisig using the embedded public key —
// the same check the updater runs before applying. A self-test that the signing
// key matches what's compiled in. Returns an error (nonzero exit) on mismatch.
func verifyFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(path + ".minisig")
	if err != nil {
		return err
	}
	key := os.Getenv("MINISIGN_PUBLIC_KEY")
	var verifyErr error
	if key == "" {
		verifyErr = update.Verify(data, sig)
	} else {
		verifyErr = update.VerifyWithKey(key, data, sig)
	}
	if verifyErr != nil {
		return verifyErr
	}
	fmt.Printf("OK: %s verifies against the embedded public key\n", path)
	return nil
}

// genKey generates a fresh minisign key pair, writing the encrypted private key
// (deepzora.key) and the public key (deepzora.pub) into dir. The password comes
// from $MINISIGN_PASSWORD. The public key is printed — it's safe to publish; embed
// it in internal/update/deepzora.pub. The private key never leaves dir.
func genKey(dir string) error {
	pw := os.Getenv("MINISIGN_PASSWORD")
	if strings.TrimSpace(pw) == "" {
		return fmt.Errorf("genkey: MINISIGN_PASSWORD is empty")
	}
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	enc, err := minisign.EncryptKey(pw, priv)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keyPath := filepath.Join(dir, "deepzora.key")
	pubPath := filepath.Join(dir, "deepzora.pub")
	if err := os.WriteFile(keyPath, enc, 0o600); err != nil {
		return err
	}
	pubText, err := pub.MarshalText()
	if err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, pubText, 0o644); err != nil {
		return err
	}
	fmt.Printf("private key -> %s (keep secret; this is the MINISIGN_PRIVATE_KEY value)\n", keyPath)
	fmt.Printf("public key  -> %s\n\n", pubPath)
	fmt.Printf("public key (save to internal/update/deepzora.pub, key ID %016X):\n%s\n", pub.ID(), pubText)
	return nil
}

// signFiles writes a detached .minisig next to each input file. The private key is
// read only from the environment — it never touches disk or argv.
func signFiles(files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("sign: no files given")
	}
	keyText := os.Getenv("MINISIGN_PRIVATE_KEY")
	if strings.TrimSpace(keyText) == "" {
		return fmt.Errorf("sign: MINISIGN_PRIVATE_KEY is empty")
	}
	priv, err := minisign.DecryptKey(os.Getenv("MINISIGN_PASSWORD"), []byte(keyText))
	if err != nil {
		return fmt.Errorf("sign: decrypt private key: %w", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		sig := minisign.SignWithComments(priv, data,
			"file:"+filepath.Base(f), "Deepzora desktop release")
		if key := os.Getenv("MINISIGN_PUBLIC_KEY"); key != "" {
			if err := update.VerifyWithKey(key, data, sig); err != nil {
				return fmt.Errorf("sign: configured public key does not match the signing key: %w", err)
			}
		}
		out := f + ".minisig"
		if err := os.WriteFile(out, sig, 0o644); err != nil {
			return err
		}
		fmt.Printf("signed %s -> %s\n", f, out)
	}
	return nil
}

// genManifest scans dir for the per-platform artifacts and writes dir/latest.json.
// version is the semver compared by the updater (e.g. "v1.1.0"); tag is the GitHub
// release tag used in download URLs (e.g. "desktop-v1.1.0").
//
// Portable updater channels land in platforms (tarballs/installers). Debian/Ubuntu
// .deb packages use native_packages; platforms selects the portable update path.
func genManifest(dir, version, tag string, notesVersions ...string) error {
	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		repo = "SivanCola/deepzora"
	}
	notesVersion := version
	if len(notesVersions) > 1 {
		return fmt.Errorf("manifest: expected at most one release-notes version")
	}
	if len(notesVersions) == 1 {
		notesVersion = notesVersions[0]
	}
	m := update.Manifest{
		Version:         version,
		DownloadPage:    "https://github.com/SivanCola/deepzora/releases",
		ReleaseNotesURL: "https://github.com/SivanCola/deepzora/releases/tag/" + notesVersion + "/",
		Platforms:       map[string]update.Asset{},
		NativePackages:  map[string]update.Asset{},
		Downloads:       map[string]update.Asset{},
	}
	m.ArtifactNaming = "versioned-v1"
	for _, spec := range releaseassets.DesktopAssets() {
		name := releaseassets.DesktopName(version, spec)
		size, sum, err := hashFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("manifest: missing or unreadable download %s: %w", name, err)
		}
		url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, name)
		asset := update.Asset{URL: url, Sig: url + ".minisig", Size: size, SHA256: sum, InstallLayout: update.ElectronInstallLayout}
		m.Downloads[name] = asset
		switch spec.Channel {
		case "platforms":
			m.Platforms[spec.Platform] = asset
		case "native_packages":
			m.NativePackages[spec.Platform] = asset
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "latest.json"), append(b, '\n'), 0o644)
}

// hashFile returns the size and lowercase-hex SHA-256 of a file, streaming it so
// large artifacts don't have to fit in memory.
func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}
