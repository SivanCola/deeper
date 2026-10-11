//go:build windows

package winaclresidue

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// canonicalWindowsPath expands 8.3 aliases without opening the protected file.
// The original spelling is kept on failure so the exact-path comparison still
// works on volumes without long-name expansion.
func canonicalWindowsPath(path string) string {
	input, err := windows.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	buffer := make([]uint16, 32768)
	n, err := windows.GetLongPathName(input, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		return filepath.Clean(path)
	}
	return filepath.Clean(windows.UTF16ToString(buffer[:n]))
}

// ResetCredentialDACL replaces the DACL of the credential store with a
// protected entry that grants only the current user full control. It opens
// the file for WRITE_DAC alone and never reads the existing descriptor, so it
// works when READ_CONTROL is denied and the caller is not the owner. Callers
// reserve it for an explicit save of Deepzora's own credential file after the
// provenance-checked repair could not run.
func ResetCredentialDACL(path string) error {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Attributes are readable through the parent directory's list right even
	// while the file itself denies FILE_READ_ATTRIBUTES.
	attrs, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return fmt.Errorf("inspect credential store %q: %w", path, err)
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refusing to reset the ACL of reparse point %q", path)
	}
	handle, err := openExact(path, windows.WRITE_DAC)
	if err != nil {
		return fmt.Errorf("open credential store for WRITE_DAC: %w", err)
	}
	defer windows.CloseHandle(handle)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if user == nil || user.User.Sid == nil {
		return fmt.Errorf("current process token has no user SID")
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.STANDARD_RIGHTS_ALL | windows.SPECIFIC_RIGHTS_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid)},
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, nil)
	if err != nil {
		return fmt.Errorf("build credential store DACL: %w", err)
	}
	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		return err
	}
	if err := sd.SetDACL(acl, true, false); err != nil {
		return err
	}
	// Protected: inherited entries from the profile directory must not bring
	// back the trustees the reset is meant to replace.
	if err := sd.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		return err
	}
	if err := windows.SetKernelObjectSecurity(handle, windows.DACL_SECURITY_INFORMATION, sd); err != nil {
		return fmt.Errorf("reset credential store DACL %q: %w", path, err)
	}
	return nil
}
