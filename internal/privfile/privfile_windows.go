//go:build windows

package privfile

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// RestrictDir gives the current user alone access to dir, and to what is
// created in it, and stops inheriting from the parent.
func RestrictDir(dir string) error {
	return restrict(dir, true)
}

// RestrictFile gives the current user alone access to path, and stops
// inheriting from the parent.
func RestrictFile(path string) error {
	return restrict(path, false)
}

// CheckPrivate reports ErrNotPrivate unless path is owned by the current
// user and every entry in its DACL is for them.
func CheckPrivate(path string) error {
	user, err := currentUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("reading the access of %s: %w", path, err)
	}
	return checkOwnerAndDACL(path, user, sd)
}

func restrict(path string, inherit bool) error {
	user, err := currentUser()
	if err != nil {
		return err
	}
	acl, err := privateACL(user, inherit)
	if err != nil {
		return fmt.Errorf("building the access of %s: %w", path, err)
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION,
		user, nil, acl, nil)
	if err != nil {
		return fmt.Errorf("setting the access of %s: %w", path, err)
	}
	return nil
}

func currentUser() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("reading the current user: %w", err)
	}
	return user.User.Sid, nil
}

func privateACL(user *windows.SID, inherit bool) (*windows.ACL, error) {
	inheritance := uint32(windows.NO_INHERITANCE)
	if inherit {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	return windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeValue: windows.TrusteeValueFromSID(user),
		},
	}}, nil)
}

func checkOwnerAndDACL(path string, user *windows.SID, sd *windows.SECURITY_DESCRIPTOR) error {
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("reading the owner of %s: %w", path, err)
	}
	if owner == nil || !owner.Equals(user) {
		return notPrivate(path)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("reading the access of %s: %w", path, err)
	}
	if dacl == nil {
		return notPrivate(path)
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("reading the access of %s: %w", path, err)
		}
		if !aceSID(ace).Equals(user) {
			return notPrivate(path)
		}
	}
	return nil
}

// aceSID is the SID that follows an access-allowed ACE's header and mask.
func aceSID(ace *windows.ACCESS_ALLOWED_ACE) *windows.SID {
	return (*windows.SID)(unsafe.Pointer(&ace.SidStart))
}

func notPrivate(path string) error {
	return fmt.Errorf("%s: %w", path, ErrNotPrivate)
}
