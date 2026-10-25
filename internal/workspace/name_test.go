package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeName(t *testing.T) {
	assertID := func(t *testing.T, name, want string) {
		t.Helper()
		assert.Equal(t, want, sanitizeName(name), "sanitizeName(%q)", name)
	}
	t.Run("lowercases and joins words", func(t *testing.T) {
		assertID(t, "Fix Login", "fix-login")
	})
	t.Run("folds accents", func(t *testing.T) {
		assertID(t, "café-x", "cafe-x")
		assertID(t, "Ångström", "angstrom")
	})
	t.Run("folds compatibility characters", func(t *testing.T) {
		assertID(t, "ﬁle №5", "file-no5")
	})
	t.Run("replaces other characters", func(t *testing.T) {
		assertID(t, "a.b_c", "a-b-c")
	})
	t.Run("letters without a decomposition become dashes", func(t *testing.T) {
		assertID(t, "Straße", "stra-e")
	})
	t.Run("trims dashes and spaces", func(t *testing.T) {
		assertID(t, "  --x--  ", "x")
	})
	t.Run("drops emoji", func(t *testing.T) {
		assertID(t, "🚀 launch", "launch")
	})
	t.Run("can't climb directories", func(t *testing.T) {
		assertID(t, "../etc", "etc")
	})
	t.Run("nothing usable is empty", func(t *testing.T) {
		assertID(t, "日本語", "")
	})
	t.Run("cuts to 55 characters", func(t *testing.T) {
		assertID(t, strings.Repeat("a", 90), strings.Repeat("a", 55))
	})
	t.Run("doesn't end a cut on a dash", func(t *testing.T) {
		assertID(t, strings.Repeat("a", 54)+" b", strings.Repeat("a", 54))
	})
}

func TestGenerateID(t *testing.T) {
	now := time.Date(2026, 10, 18, 23, 0, 0, 0, time.UTC)
	assert.Equal(t, "ws-20261018-k3f9qa", generateID(now, "k3f9qa"))
}

func TestNamedID(t *testing.T) {
	t.Parallel()
	t.Run("ends with the suffix", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "this-is-a-test-d8efb24f", namedID(sanitizeName("This is a Test"), "d8efb24f"))
	})
	t.Run("the longest name fits in 64 characters", func(t *testing.T) {
		t.Parallel()
		assert.Len(t, namedID(sanitizeName(strings.Repeat("a", 90)), "d8efb24f"), maxIDLen)
	})
}

func TestRandomHex(t *testing.T) {
	t.Parallel()
	suffix, err := randomHex()
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9a-f]{8}$`, suffix)
}

func TestRandomSuffix(t *testing.T) {
	suffix, err := randomSuffix()
	require.NoError(t, err)
	assert.Len(t, suffix, 6)
	assert.Equal(t, suffix, sanitizeName(suffix), "only [a-z0-9]")
}
