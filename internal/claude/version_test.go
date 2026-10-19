package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMinVersion(t *testing.T) {
	assert.Equal(t, version{major: 2, minor: 1, patch: 302}, minVersion())
}

func TestVersion_String(t *testing.T) {
	assert.Equal(t, "2.1.303", version{major: 2, minor: 1, patch: 303}.String())
}

func TestVersion_atLeast(t *testing.T) {
	v := version{major: 2, minor: 1, patch: 302}
	t.Run("equal", func(t *testing.T) {
		assert.True(t, v.atLeast(v))
	})
	t.Run("newer major", func(t *testing.T) {
		assert.True(t, version{major: 3}.atLeast(v))
	})
	t.Run("older major", func(t *testing.T) {
		assert.False(t, version{major: 1, minor: 99, patch: 999}.atLeast(v))
	})
	t.Run("newer minor", func(t *testing.T) {
		assert.True(t, version{major: 2, minor: 2}.atLeast(v))
	})
	t.Run("older minor", func(t *testing.T) {
		assert.False(t, version{major: 2, minor: 0, patch: 999}.atLeast(v))
	})
	t.Run("newer patch", func(t *testing.T) {
		assert.True(t, version{major: 2, minor: 1, patch: 303}.atLeast(v))
	})
	t.Run("older patch", func(t *testing.T) {
		assert.False(t, version{major: 2, minor: 1, patch: 84}.atLeast(v))
	})
}

func TestParseVersion(t *testing.T) {
	t.Run("release", func(t *testing.T) {
		v, err := parseVersion("2.1.303 (Claude Code)\n")
		require.NoError(t, err)
		assert.Equal(t, version{major: 2, minor: 1, patch: 303}, v)
	})
	t.Run("bare number", func(t *testing.T) {
		v, err := parseVersion("2.1.84")
		require.NoError(t, err)
		assert.Equal(t, version{major: 2, minor: 1, patch: 84}, v)
	})
	t.Run("pre-release", func(t *testing.T) {
		v, err := parseVersion("2.1.304-beta.1 (Claude Code)")
		require.NoError(t, err)
		assert.Equal(t, version{major: 2, minor: 1, patch: 304}, v)
	})
	t.Run("build metadata", func(t *testing.T) {
		v, err := parseVersion("2.1.304+abc")
		require.NoError(t, err)
		assert.Equal(t, version{major: 2, minor: 1, patch: 304}, v)
	})
	t.Run("rejects empty output", func(t *testing.T) {
		_, err := parseVersion("")
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("rejects two parts", func(t *testing.T) {
		_, err := parseVersion("2.1 (Claude Code)")
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("rejects non-numeric parts", func(t *testing.T) {
		_, err := parseVersion("2.x.1 (Claude Code)")
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("rejects negative parts", func(t *testing.T) {
		_, err := parseVersion("2.-1.1")
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
}
