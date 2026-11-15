package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestSecrets(t *testing.T, dir, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, SecretsFile)
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
	require.NoError(t, os.Chmod(path, mode))
}

func TestReadSecrets(t *testing.T) {
	t.Parallel()
	t.Run("skips comments and blanks, strips quotes, splits at the first =", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestSecrets(t, dir, "# tokens\n\nA=plain\nB=\"quoted value\"\nC=b=c\n", 0o600)
		secrets, warnings, err := ReadSecrets(dir)
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Equal(t, map[string]string{"A": "plain", "B": "quoted value", "C": "b=c"}, secrets)
	})
	t.Run("a missing file is empty", func(t *testing.T) {
		t.Parallel()
		secrets, warnings, err := ReadSecrets(t.TempDir())
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Empty(t, secrets)
	})
	t.Run("a mode looser than 0600 is ignored with a warning", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestSecrets(t, dir, "A=1\n", 0o644)
		secrets, warnings, err := ReadSecrets(dir)
		require.NoError(t, err)
		assert.Empty(t, secrets)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "0644")
	})
	t.Run("a line without = is an error naming its line number, not its text", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestSecrets(t, dir, "A=1\nsecret-looking-text\n", 0o600)
		_, _, err := ReadSecrets(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "line 2")
		assert.NotContains(t, err.Error(), "secret-looking-text")
	})
}

func TestSetSecret(t *testing.T) {
	t.Parallel()
	t.Run("adds a line to a new file with mode 0600", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, SetSecret(dir, "METRICS_TOKEN", "abc"))
		data, err := os.ReadFile(filepath.Join(dir, SecretsFile))
		require.NoError(t, err)
		assert.Equal(t, "METRICS_TOKEN=abc\n", string(data))
		assertSecretsMode(t, dir, 0o600)
	})
	t.Run("replaces a line in place, keeping comments and order", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestSecrets(t, dir, "# head\nA=1\n# mid\nB=2\n", 0o600)
		require.NoError(t, SetSecret(dir, "A", "9"))
		assertSecretsContent(t, dir, "# head\nA=9\n# mid\nB=2\n")
	})
	t.Run("an empty value removes the line", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestSecrets(t, dir, "# head\nA=1\n# mid\nB=2\n", 0o600)
		require.NoError(t, SetSecret(dir, "B", ""))
		assertSecretsContent(t, dir, "# head\nA=1\n# mid\n")
	})
	t.Run("a loose file is rewritten with mode 0600", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestSecrets(t, dir, "A=1\n", 0o644)
		require.NoError(t, SetSecret(dir, "B", "2"))
		assertSecretsMode(t, dir, 0o600)
	})
	t.Run("rejects a name that is not NAME-like", func(t *testing.T) {
		t.Parallel()
		err := SetSecret(t.TempDir(), "bad-name", "x")
		require.Error(t, err)
	})
	t.Run("rejects a value with a line break", func(t *testing.T) {
		t.Parallel()
		err := SetSecret(t.TempDir(), "A", "x\ny")
		require.Error(t, err)
	})
}

func assertSecretsContent(t *testing.T, dir, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, SecretsFile))
	require.NoError(t, err)
	assert.Equal(t, want, string(data))
}

func assertSecretsMode(t *testing.T, dir string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, SecretsFile))
	require.NoError(t, err)
	assert.Equal(t, want, info.Mode().Perm())
}
