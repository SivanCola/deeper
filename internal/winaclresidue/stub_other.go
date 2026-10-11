//go:build !windows

package winaclresidue

import (
	"errors"
	"os"
)

// ResetCredentialDACL has no equivalent outside Windows.
func ResetCredentialDACL(string) error { return errors.ErrUnsupported }

// RenameLockedFile is an ordinary rename outside Windows.
func RenameLockedFile(path, target string) error { return os.Rename(path, target) }
