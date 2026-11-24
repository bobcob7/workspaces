package gate

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileInfo is a stand-in for an existing file; findSh only checks the error.
type fileInfo struct {
	os.FileInfo
}

func fakeLookPath(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := found[name]; ok {
			return path, nil
		}
		return "", exec.ErrNotFound
	}
}

func fakeStat(present ...string) func(string) (os.FileInfo, error) {
	return func(name string) (os.FileInfo, error) {
		if slices.Contains(present, name) {
			return fileInfo{}, nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestFindSh(t *testing.T) {
	gitRoot := `C:\Git`
	gitExe := filepath.Join(gitRoot, "cmd", "git.exe")
	binSh := filepath.Join(gitRoot, "bin", "sh.exe")
	usrSh := filepath.Join(gitRoot, "usr", "bin", "sh.exe")
	t.Run("sh on PATH wins", func(t *testing.T) {
		sh, err := findSh(fakeLookPath(map[string]string{"sh": `C:\tools\sh.exe`, "git": gitExe}), fakeStat(binSh, usrSh))
		require.NoError(t, err)
		assert.Equal(t, `C:\tools\sh.exe`, sh)
	})
	t.Run("sh beside git in bin", func(t *testing.T) {
		sh, err := findSh(fakeLookPath(map[string]string{"git": gitExe}), fakeStat(binSh, usrSh))
		require.NoError(t, err)
		assert.Equal(t, binSh, sh)
	})
	t.Run("sh beside git in usr\\bin", func(t *testing.T) {
		sh, err := findSh(fakeLookPath(map[string]string{"git": gitExe}), fakeStat(usrSh))
		require.NoError(t, err)
		assert.Equal(t, usrSh, sh)
	})
	t.Run("no sh anywhere is ErrNoShell", func(t *testing.T) {
		_, err := findSh(fakeLookPath(map[string]string{"git": gitExe}), fakeStat())
		assert.ErrorIs(t, err, ErrNoShell)
	})
	t.Run("no git and no sh is ErrNoShell", func(t *testing.T) {
		_, err := findSh(fakeLookPath(nil), fakeStat(binSh, usrSh))
		assert.ErrorIs(t, err, ErrNoShell)
	})
}
