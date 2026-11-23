package artifact

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocate(t *testing.T) {
	t.Run("anchors the only occurrence", func(t *testing.T) {
		text := "one\ntwo three\nfour\n"
		a, err := Locate(text, 2, AnchorRequest{Quote: "three"})
		require.NoError(t, err)
		assert.Equal(t, Anchor{
			Rev: 2, Start: 8, End: 13, LineStart: 2, LineEnd: 2, Quote: "three",
			Prefix: "one\ntwo ", Suffix: "\nfour\n", State: "anchored",
		}, a)
	})
	t.Run("picks the occurrence starting on the given line", func(t *testing.T) {
		text := "cache\nthe cache\n"
		a, err := Locate(text, 1, AnchorRequest{Quote: "cache", Line: 2})
		require.NoError(t, err)
		assert.Equal(t, 2, a.LineStart)
		assert.Equal(t, 10, a.Start)
	})
	t.Run("picks the occurrence with the highest context score", func(t *testing.T) {
		text := "a red fox\nand a blue fox\n"
		a, err := Locate(text, 1, AnchorRequest{Quote: "fox", Prefix: "blue "})
		require.NoError(t, err)
		assert.Equal(t, 2, a.LineStart)
	})
	t.Run("picks by suffix context", func(t *testing.T) {
		text := "x fox runs\ny fox sits\n"
		a, err := Locate(text, 1, AnchorRequest{Quote: "fox", Suffix: " sits"})
		require.NoError(t, err)
		assert.Equal(t, 2, a.LineStart)
	})
	t.Run("refuses occurrences with equal scores", func(t *testing.T) {
		text := "fox\nfox\nfox\n"
		_, err := Locate(text, 1, AnchorRequest{Quote: "fox"})
		require.ErrorIs(t, err, ErrAnchorNotFound)
		var anchorErr *AnchorError
		require.ErrorAs(t, err, &anchorErr)
		assert.Equal(t, 3, anchorErr.Occurrences)
		assert.Equal(t, []int{1, 2, 3}, anchorErr.Lines)
	})
	t.Run("refuses a line with no occurrence and no context", func(t *testing.T) {
		text := "fox\nfox\n"
		_, err := Locate(text, 1, AnchorRequest{Quote: "fox", Line: 9})
		var anchorErr *AnchorError
		require.ErrorAs(t, err, &anchorErr)
		assert.Equal(t, 2, anchorErr.Occurrences)
	})
	t.Run("reports a missing quote with no occurrences", func(t *testing.T) {
		_, err := Locate("one\n", 1, AnchorRequest{Quote: "two"})
		require.ErrorIs(t, err, ErrAnchorNotFound)
		var anchorErr *AnchorError
		require.ErrorAs(t, err, &anchorErr)
		assert.Equal(t, 0, anchorErr.Occurrences)
		assert.Empty(t, anchorErr.Lines)
	})
	t.Run("refuses an empty or whitespace quote", func(t *testing.T) {
		_, err := Locate("one", 1, AnchorRequest{Quote: ""})
		require.ErrorIs(t, err, ErrInvalid)
		_, err = Locate("one", 1, AnchorRequest{Quote: " \n\t "})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a quote over the limit", func(t *testing.T) {
		_, err := Locate(strings.Repeat("a", MaxQuoteRunes+1), 1, AnchorRequest{Quote: strings.Repeat("a", MaxQuoteRunes+1)})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("is deterministic", func(t *testing.T) {
		text := "fox\nfox\nfox\n"
		req := AnchorRequest{Quote: "fox", Line: 2}
		first, err1 := Locate(text, 1, req)
		second, err2 := Locate(text, 1, req)
		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.Equal(t, first, second)
	})
}

func TestMove(t *testing.T) {
	base := "alpha\nthe cache is shared\nomega\n"
	anchored, err := Locate(base, 1, AnchorRequest{Quote: "the cache is shared"})
	require.NoError(t, err)

	t.Run("follows the quote after an edit above it", func(t *testing.T) {
		text := "new line\n" + base
		moved, ok := Move(text, 2, anchored)
		require.True(t, ok)
		assert.Equal(t, 2, moved.Rev)
		assert.Equal(t, 3, moved.LineStart)
		assert.Equal(t, anchored.Start+len("new line\n"), moved.Start)
		assert.Equal(t, "the cache is shared", moved.Quote)
		assert.Equal(t, "anchored", moved.State)
	})
	t.Run("is gone when the quote's own text changed", func(t *testing.T) {
		text := "alpha\nthe cache is local\nomega\n"
		_, ok := Move(text, 2, anchored)
		assert.False(t, ok)
	})
	t.Run("finds the quote with its whitespace reflowed", func(t *testing.T) {
		text := "alpha\nthe cache\nis shared\nomega\n"
		moved, ok := Move(text, 2, anchored)
		require.True(t, ok)
		assert.Equal(t, 2, moved.LineStart)
		assert.Equal(t, 3, moved.LineEnd)
		assert.Equal(t, "the cache is shared", moved.Quote)
		assert.Equal(t, "the cache\nis shared", text[moved.Start:moved.End])
	})
	t.Run("picks among several occurrences by context", func(t *testing.T) {
		old := Anchor{Quote: "fox", Prefix: "red ", Suffix: " jumps", LineStart: 1}
		moved, ok := Move("a fox sits\nthe red fox jumps\n", 2, old)
		require.True(t, ok)
		assert.Equal(t, 2, moved.LineStart)
	})
	t.Run("picks the line closest to the old line when context ties", func(t *testing.T) {
		old := Anchor{Quote: "fox", LineStart: 3}
		moved, ok := Move("fox\nfox\nfox\nfox\nfox\n", 2, old)
		require.True(t, ok)
		assert.Equal(t, 3, moved.LineStart)
	})
	t.Run("picks the earliest when line and context tie", func(t *testing.T) {
		old := Anchor{Quote: "fox", LineStart: 2}
		moved, ok := Move("fox\nx\nfox\n", 2, old)
		require.True(t, ok)
		assert.Equal(t, 1, moved.LineStart)
	})
	t.Run("refuses an empty quote", func(t *testing.T) {
		_, ok := Move("text", 2, Anchor{})
		assert.False(t, ok)
	})
	t.Run("is deterministic", func(t *testing.T) {
		text := "fox\nfox\n"
		old := Anchor{Quote: "fox", LineStart: 2}
		first, ok1 := Move(text, 2, old)
		second, ok2 := Move(text, 2, old)
		require.True(t, ok1)
		require.True(t, ok2)
		assert.Equal(t, first, second)
	})
}

func TestAnchorError_Error(t *testing.T) {
	t.Run("lists the occurrences and their lines", func(t *testing.T) {
		err := &AnchorError{Occurrences: 3, Lines: []int{4, 9, 12}}
		assert.Equal(t, "the quote occurs 3 times, on lines 4, 9, 12; pass --line", err.Error())
	})
	t.Run("says the quote is gone when there are none", func(t *testing.T) {
		err := &AnchorError{}
		assert.Equal(t, "the quote isn't in the latest revision", err.Error())
	})
}

func TestAnchorError_Is(t *testing.T) {
	err := &AnchorError{Occurrences: 2}
	require.ErrorIs(t, err, ErrAnchorNotFound)
	assert.NotErrorIs(t, err, ErrInvalid)
}

func TestOccurrences(t *testing.T) {
	assert.Equal(t, []int{0, 3}, occurrences("ab ab", "ab"))
	assert.Equal(t, []int{0, 1}, occurrences("aaa", "aa"))
	assert.Nil(t, occurrences("abc", ""))
	assert.Nil(t, occurrences("abc", "x"))
}

func TestNormalizedOccurrences(t *testing.T) {
	t.Run("collapses whitespace on both sides", func(t *testing.T) {
		text := "a  b\n c"
		spans := normalizedOccurrences(text, "a b c")
		require.Len(t, spans, 1)
		assert.Equal(t, "a  b\n c", text[spans[0][0]:spans[0][1]])
	})
	t.Run("returns nothing for a whitespace-only quote", func(t *testing.T) {
		assert.Nil(t, normalizedOccurrences("a b", " \n "))
	})
	t.Run("maps multi-byte text back to byte offsets", func(t *testing.T) {
		text := "café au lait 🐈"
		spans := normalizedOccurrences(text, "au lait")
		require.Len(t, spans, 1)
		assert.Equal(t, "au lait", text[spans[0][0]:spans[0][1]])
	})
	t.Run("returns nothing when the quote is absent", func(t *testing.T) {
		assert.Empty(t, normalizedOccurrences("a b", "c"))
	})
}

func TestContextScore(t *testing.T) {
	text := "the red fox jumps"
	assert.Equal(t, 4, contextScore(text, 8, 11, "red ", ""))
	assert.Equal(t, 6, contextScore(text, 8, 11, "", " jumps"))
	assert.Equal(t, 10, contextScore(text, 8, 11, "red ", " jumps"))
	assert.Equal(t, 1, contextScore(text, 8, 11, "blue ", "xx"))
}

func TestLineAt(t *testing.T) {
	text := "one\ntwo\nthree"
	assert.Equal(t, 1, lineAt(text, 0))
	assert.Equal(t, 2, lineAt(text, 4))
	assert.Equal(t, 3, lineAt(text, 8))
}

func TestContextAround(t *testing.T) {
	t.Run("takes up to ContextRunes runes on each side", func(t *testing.T) {
		text := strings.Repeat("a", 40) + "fox" + strings.Repeat("b", 40)
		prefix, suffix := contextAround(text, 40, 43)
		assert.Equal(t, strings.Repeat("a", ContextRunes), prefix)
		assert.Equal(t, strings.Repeat("b", ContextRunes), suffix)
	})
	t.Run("counts runes, not bytes", func(t *testing.T) {
		text := "éé🐈fox"
		prefix, suffix := contextAround(text, len("éé🐈"), len("éé🐈fox"))
		assert.Equal(t, "éé🐈", prefix)
		assert.Empty(t, suffix)
	})
}

func TestAnchorAt(t *testing.T) {
	t.Run("fills every field for a span over lines", func(t *testing.T) {
		text := "one\ntwo\nthree\n"
		a := anchorAt(text, 3, 4, 11)
		assert.Equal(t, Anchor{
			Rev: 3, Start: 4, End: 11, LineStart: 2, LineEnd: 3, Quote: "two\nthr",
			Prefix: "one\n", Suffix: "ee\n", State: "anchored",
		}, a)
	})
	t.Run("uses the start line for an empty span", func(t *testing.T) {
		a := anchorAt("one\ntwo", 1, 5, 5)
		assert.Equal(t, 2, a.LineStart)
		assert.Equal(t, 2, a.LineEnd)
	})
}
