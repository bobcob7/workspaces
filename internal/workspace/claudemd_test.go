package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeMD(t *testing.T) {
	t.Run("tells agents to run ws llm", func(t *testing.T) {
		assert.Contains(t, string(claudeMD), "run `ws llm`")
	})
	t.Run("ends with a newline", func(t *testing.T) {
		assert.True(t, strings.HasSuffix(string(claudeMD), "\n"))
	})
}

func TestStore_writeClaudeMD(t *testing.T) {
	t.Parallel()
	t.Run("writes CLAUDE.md under the workspace root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		s.writeClaudeMD()
		data, err := os.ReadFile(filepath.Join(s.root, "CLAUDE.md"))
		require.NoError(t, err)
		assert.Equal(t, claudeMD, data)
		assert.Empty(t, s.Warnings())
	})
	t.Run("never overwrites an edited CLAUDE.md", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		path := filepath.Join(s.root, "CLAUDE.md")
		writeFile(t, path, "# mine\n")
		s.writeClaudeMD()
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "# mine\n", string(data))
	})
	t.Run("a failure is a warning naming the file", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		var gotPath string
		s.writeOnce = func(path string, _ []byte) error {
			gotPath = path
			return errTest
		}
		s.writeClaudeMD()
		assert.Equal(t, filepath.Join(s.root, "CLAUDE.md"), gotPath)
		require.Len(t, s.Warnings(), 1)
		assert.Contains(t, s.Warnings()[0], gotPath)
		assert.Contains(t, s.Warnings()[0], errTest.Error())
	})
}
