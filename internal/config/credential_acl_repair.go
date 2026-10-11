package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	fileencoding "deepzora/internal/fileutil/encoding"
	"deepzora/internal/winaclresidue"
)

// Test seams for the writer-side recovery ladder.
var (
	credentialStoreReset      = winaclresidue.ResetCredentialDACL
	credentialStoreQuarantine = quarantineCredentialStore
)

func readCredentialFile(path string) ([]byte, error) { return os.ReadFile(path) }

// readCredentialFileForWrite serves an explicit save. Saving must not depend
// on proving who locked the file: it resets the DACL without reading it and,
// failing that, moves the locked file
// aside and starts a fresh store. The old file stays next to it for recovery.
func readCredentialFileForWrite(path string) ([]byte, error) {
	data, err := readCredentialFile(path)
	if err == nil || !errors.Is(err, fs.ErrPermission) || runtimeGOOS != "windows" || !isGlobalCredentialPath(path) {
		return data, err
	}
	repairPath := credentialRepairPath(path)
	if resetErr := credentialStoreReset(repairPath); resetErr != nil {
		slog.Warn("config: credential store ACL reset failed", "path", path, "err", resetErr)
	} else if data, readErr := os.ReadFile(path); readErr == nil {
		slog.Warn("config: reset the credential store ACL to the current user", "path", path)
		return data, nil
	}
	quarantined, quarantineErr := credentialStoreQuarantine(repairPath)
	if quarantineErr != nil {
		return nil, fmt.Errorf("%w; the credential store could not be reset or moved aside: %w", err, quarantineErr)
	}
	slog.Warn("config: moved the locked credential store aside and started a new one", "path", path, "quarantined", quarantined)
	return nil, nil
}

func isGlobalCredentialPath(path string) bool {
	credentials := strings.TrimSpace(UserCredentialsPath())
	return path != "" && credentials != "" && samePath(path, credentials)
}

// credentialRepairPath resolves the denied credential file before a current
// explicit save repairs it. Ordinary reads do not resolve the extra path.
func credentialRepairPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// quarantineCredentialStore keeps the locked file beside the store under a
// timestamped name. The move needs DELETE on the file plus directory rights,
// neither of which a read deny removes.
func quarantineCredentialStore(path string) (string, error) {
	target := path + ".locked-" + time.Now().UTC().Format("20060102-150405")
	if err := winaclresidue.RenameLockedFile(path, target); err != nil {
		return "", err
	}
	return target, nil
}

func readCredentialFileLines(path string) ([]string, error) {
	return splitCredentialLines(readCredentialFile(path))
}

// readCredentialFileLinesForWrite is the writer-side reader: an explicit save
// may recover a locked store, which plain reads must never attempt.
func readCredentialFileLinesForWrite(path string) ([]string, error) {
	return splitCredentialLines(readCredentialFileForWrite(path))
}

func splitCredentialLines(data []byte, err error) ([]string, error) {
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	text := strings.TrimRight(string(fileencoding.DecodeToUTF8(data)), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}
