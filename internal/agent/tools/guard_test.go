package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub", ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "accepts a file inside", path: "a.txt"},
		{name: "accepts a new file in a new directory", path: "new/dir/f.txt"},
		{name: "refuses a path above the workspace", path: "../x", wantErr: "outside the workspace"},
		{name: "refuses a symlink to outside", path: "escape/f", wantErr: "outside the workspace"},
		{name: "refuses .git at the top", path: ".git/config", wantErr: "under .git"},
		{name: "refuses .git below a directory", path: "sub/.git/HEAD", wantErr: "under .git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := CheckPath(root, root, tt.path)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
	t.Run("refuses a dangling symlink", func(t *testing.T) {
		t.Parallel()
		dangling := t.TempDir()
		require.NoError(t, os.Symlink(filepath.Join(outside, "gone"), filepath.Join(dangling, "link")))
		_, err := CheckPath(dangling, dangling, "link/f")
		require.Error(t, err)
	})
}

func TestCheckCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		branch  string
		wantErr string
	}{
		{name: "refuses ws list", command: "ws list", branch: "feat/x", wantErr: "command refused"},
		{name: "refuses ws machine spawn", command: "ws machine spawn x", branch: "feat/x", wantErr: "command refused"},
		{name: "refuses claude", command: "claude logs", branch: "feat/x", wantErr: "command refused"},
		{name: "refuses sudo", command: "sudo ls", branch: "feat/x", wantErr: "command refused"},
		{name: "refuses a push to main", command: "git push origin main", branch: "feat/x", wantErr: "git push to main"},
		{name: "refuses a push to master via refspec", command: "git push origin HEAD:master", branch: "feat/x", wantErr: "git push to master"},
		{name: "refuses a force push to another branch", command: "git push -f origin feat/y", branch: "feat/x", wantErr: "only \"feat/x\" is allowed"},
		{name: "refuses a forced push with no branch", command: "git push -f", branch: "feat/x", wantErr: "explicit branch"},
		{name: "refuses ws rm chained after make", command: "make test && ws rm a", branch: "feat/x", wantErr: "command refused"},
		{name: "allows ws machine result", command: "ws machine result PASS --file ../r.md", branch: "feat/x"},
		{name: "allows a force push to the role branch", command: "git push -f -u origin feat/x", branch: "feat/x"},
		{name: "allows go test", command: "go test ./...", branch: "feat/x"},
		{name: "allows a force push chained after a test", command: "go test ./... && git push -f origin feat/x", branch: "feat/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckCommand(tt.command, tt.branch)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
