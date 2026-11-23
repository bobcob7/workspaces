package artifact

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewID(t *testing.T) {
	t.Run("formats the random bytes as an id", func(t *testing.T) {
		id, err := NewID(bytes.NewReader([]byte{0x1a, 0x2b, 0x3c, 0x4d}))
		require.NoError(t, err)
		assert.Equal(t, "art-1a2b3c4d", id)
	})
	t.Run("fails when the randomness runs out", func(t *testing.T) {
		_, err := NewID(bytes.NewReader([]byte{0x01}))
		require.Error(t, err)
	})
}

func TestValidID(t *testing.T) {
	t.Run("accepts an id", func(t *testing.T) {
		assert.True(t, ValidID("art-00ff12ab"))
	})
	t.Run("rejects ids that are malformed", func(t *testing.T) {
		assert.False(t, ValidID("art-00FF12AB"))
		assert.False(t, ValidID("art-00ff12a"))
		assert.False(t, ValidID("art-00ff12abc"))
		assert.False(t, ValidID("../art-00ff12ab"))
	})
}

func TestParseRef(t *testing.T) {
	t.Run("accepts a bare id", func(t *testing.T) {
		id, rev, err := ParseRef("art-1a2b3c4d")
		require.NoError(t, err)
		assert.Equal(t, "art-1a2b3c4d", id)
		assert.Equal(t, 0, rev)
	})
	t.Run("accepts a reference", func(t *testing.T) {
		id, rev, err := ParseRef("@artifact:art-1a2b3c4d")
		require.NoError(t, err)
		assert.Equal(t, "art-1a2b3c4d", id)
		assert.Equal(t, 0, rev)
	})
	t.Run("accepts a reference to a revision", func(t *testing.T) {
		id, rev, err := ParseRef("@artifact:art-1a2b3c4d/r12")
		require.NoError(t, err)
		assert.Equal(t, "art-1a2b3c4d", id)
		assert.Equal(t, 12, rev)
	})
	t.Run("rejects anything else", func(t *testing.T) {
		_, _, err := ParseRef("@artifact:art-1a2b3c4d/r0")
		require.ErrorIs(t, err, ErrInvalid)
		_, _, err = ParseRef("pitch.md")
		require.ErrorIs(t, err, ErrInvalid)
	})
}

func TestFindRefs(t *testing.T) {
	t.Run("returns distinct ids in first-seen order", func(t *testing.T) {
		text := "see @artifact:art-00000002 and @artifact:art-00000001/r3, then @artifact:art-00000002 again"
		assert.Equal(t, []string{"art-00000002", "art-00000001"}, FindRefs(text))
	})
	t.Run("is empty when there are no references", func(t *testing.T) {
		assert.Empty(t, FindRefs("art-00000001 without the prefix"))
	})
}

func TestHintLine(t *testing.T) {
	t.Run("names every id once", func(t *testing.T) {
		want := `(Artifacts art-00000001, art-00000002: run "ws machine artifact get <id>" for each, then read the file at the "path" it prints.)`
		assert.Equal(t, want, HintLine([]string{"art-00000001", "art-00000002"}))
	})
}

func TestAppendHint(t *testing.T) {
	t.Run("appends the hint to a prompt that references artifacts", func(t *testing.T) {
		prompt, ids := AppendHint("review @artifact:art-00000001")
		assert.Equal(t, []string{"art-00000001"}, ids)
		assert.Equal(t, "review @artifact:art-00000001\n\n"+HintLine(ids), prompt)
	})
	t.Run("leaves a prompt without references alone", func(t *testing.T) {
		prompt, ids := AppendHint("review the diff")
		assert.Equal(t, "review the diff", prompt)
		assert.Empty(t, ids)
	})
	t.Run("does not add a second hint", func(t *testing.T) {
		first, _ := AppendHint("review @artifact:art-00000001")
		second, _ := AppendHint(first)
		assert.Equal(t, first, second)
	})
}
