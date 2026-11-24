package privfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// daclOf reads path's owner and DACL.
func daclOf(t *testing.T, path string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	return sd
}

// requireOnlyUser checks that the DACL has one entry, for user.
func requireOnlyUser(t *testing.T, sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) {
	t.Helper()
	acl, _, err := sd.DACL()
	require.NoError(t, err)
	require.NotNil(t, acl)
	require.Equal(t, 1, int(acl.AceCount))
	var ace *windows.ACCESS_ALLOWED_ACE
	require.NoError(t, windows.GetAce(acl, 0, &ace))
	assert.True(t, aceSID(ace).Equals(user))
}

func TestRestrictDir(t *testing.T) {
	t.Parallel()
	user, err := currentUser()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, RestrictDir(dir))
	sd := daclOf(t, dir)
	control, _, err := sd.Control()
	require.NoError(t, err)
	assert.NotZero(t, control&windows.SE_DACL_PROTECTED)
	requireOnlyUser(t, sd, user)
	file := filepath.Join(dir, "inside")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	// Not CheckPrivate: a new file's owner can be Administrators on elevated windows-latest runners.
	requireOnlyUser(t, daclOf(t, file), user)
}

func TestCheckPrivate(t *testing.T) {
	t.Parallel()
	t.Run("a directory that inherits its parent's access is not private", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "inherited")
		require.NoError(t, os.Mkdir(dir, 0o700))
		err := CheckPrivate(dir)
		require.ErrorIs(t, err, ErrNotPrivate)
	})
	t.Run("a directory restricted by RestrictDir is private", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, RestrictDir(dir))
		assert.NoError(t, CheckPrivate(dir))
	})
}

func TestCheckDACL(t *testing.T) {
	t.Parallel()
	user, err := currentUser()
	require.NoError(t, err)
	t.Run("a file restricted by RestrictFile has a private DACL", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "private")
		require.NoError(t, os.WriteFile(file, nil, 0o600))
		require.NoError(t, RestrictFile(file))
		dacl, _, err := daclOf(t, file).DACL()
		require.NoError(t, err)
		assert.NoError(t, checkDACL(file, user, dacl))
	})
	t.Run("a null DACL is not private", func(t *testing.T) {
		t.Parallel()
		sd, err := windows.NewSecurityDescriptor()
		require.NoError(t, err)
		require.NoError(t, sd.SetOwner(user, false))
		require.NoError(t, sd.SetDACL(nil, true, false))
		assert.ErrorIs(t, checkOwnerAndDACL("null", user, sd), ErrNotPrivate)
	})
	t.Run("a deny entry is not private", func(t *testing.T) {
		t.Parallel()
		dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
			AccessPermissions: windows.GENERIC_READ,
			AccessMode:        windows.DENY_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeValue: windows.TrusteeValueFromSID(user),
			},
		}}, nil)
		require.NoError(t, err)
		err = checkDACL("deny", user, dacl)
		require.ErrorIs(t, err, ErrNotPrivate)
		assert.ErrorContains(t, err, "not an allow entry")
	})
}
