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
	requireOnlyUser(t, daclOf(t, file), user)
}

func TestCheckPrivate(t *testing.T) {
	t.Parallel()
	t.Run("a directory that inherits its parent's access is not private", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "inherited")
		require.NoError(t, os.Mkdir(dir, 0o700))
		err := CheckPrivate(dir)
		assert.ErrorIs(t, err, ErrNotPrivate)
	})
}
