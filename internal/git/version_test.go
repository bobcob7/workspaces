package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMinVersion(t *testing.T) {
	assert.Equal(t, Version{Major: 2, Minor: 23}, minVersion())
}

func TestVersion_String(t *testing.T) {
	assert.Equal(t, "2.50.1", Version{Major: 2, Minor: 50, Patch: 1}.String())
}

func TestVersion_atLeast(t *testing.T) {
	v := Version{Major: 2, Minor: 23, Patch: 1}
	t.Run("equal", func(t *testing.T) {
		assert.True(t, v.atLeast(v))
	})
	t.Run("newer major", func(t *testing.T) {
		assert.True(t, Version{Major: 3}.atLeast(v))
	})
	t.Run("older major", func(t *testing.T) {
		assert.False(t, Version{Major: 1, Minor: 99}.atLeast(v))
	})
	t.Run("newer minor", func(t *testing.T) {
		assert.True(t, Version{Major: 2, Minor: 24}.atLeast(v))
	})
	t.Run("older minor", func(t *testing.T) {
		assert.False(t, Version{Major: 2, Minor: 22, Patch: 9}.atLeast(v))
	})
	t.Run("newer patch", func(t *testing.T) {
		assert.True(t, Version{Major: 2, Minor: 23, Patch: 2}.atLeast(v))
	})
	t.Run("older patch", func(t *testing.T) {
		assert.False(t, Version{Major: 2, Minor: 23}.atLeast(v))
	})
}

func TestParseVersion(t *testing.T) {
	t.Run("Linux", func(t *testing.T) {
		v, err := parseVersion("git version 2.45.4\n")
		require.NoError(t, err)
		assert.Equal(t, Version{Major: 2, Minor: 45, Patch: 4}, v)
	})
	t.Run("Apple suffix", func(t *testing.T) {
		v, err := parseVersion("git version 2.50.1 (Apple Git-155)\n")
		require.NoError(t, err)
		assert.Equal(t, Version{Major: 2, Minor: 50, Patch: 1}, v)
	})
	t.Run("Windows suffix", func(t *testing.T) {
		v, err := parseVersion("git version 2.44.0.windows.1\n")
		require.NoError(t, err)
		assert.Equal(t, Version{Major: 2, Minor: 44}, v)
	})
	t.Run("no patch number", func(t *testing.T) {
		v, err := parseVersion("git version 2.23")
		require.NoError(t, err)
		assert.Equal(t, Version{Major: 2, Minor: 23}, v)
	})
	t.Run("rejects other output", func(t *testing.T) {
		_, err := parseVersion("hub version 2.14.2")
		assert.ErrorContains(t, err, "unrecognized")
	})
	t.Run("rejects empty output", func(t *testing.T) {
		_, err := parseVersion("")
		assert.Error(t, err)
	})
	t.Run("rejects a single number", func(t *testing.T) {
		_, err := parseVersion("git version 2")
		assert.Error(t, err)
	})
	t.Run("rejects non-numeric parts", func(t *testing.T) {
		_, err := parseVersion("git version 2.x.1")
		assert.Error(t, err)
	})
}
