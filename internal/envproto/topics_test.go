package envproto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTopic(t *testing.T) {
	t.Run("joins the prefix, id and levels", func(t *testing.T) {
		assert.Equal(t, "ws/v1/env/env-1a2b3c4d/res/r1", Topic("env-1a2b3c4d", LeafRes, "r1"))
	})
	t.Run("accepts no levels", func(t *testing.T) {
		assert.Equal(t, "ws/v1/env/env-1a2b3c4d/", Topic("env-1a2b3c4d"))
	})
}

func TestParseTopic(t *testing.T) {
	t.Run("round-trips a topic built by Topic", func(t *testing.T) {
		id, rest, ok := ParseTopic(Topic("env-1a2b3c4d", LeafRes, "r1"))
		assert.True(t, ok)
		assert.Equal(t, "env-1a2b3c4d", id)
		assert.Equal(t, []string{LeafRes, "r1"}, rest)
	})
	t.Run("refuses other topics", func(t *testing.T) {
		for _, topic := range []string{
			"ws/v1/env/+/status",
			"ws/v1/env/env-XYZ/status",
			"ws/v1/other",
			"ws/v1/env/env-1a2b3c4d",
			"ws/v1/env/env-1a2b3c4d/",
			"ws/v1/env/env-1a2b3c4d//status",
			"",
		} {
			_, _, ok := ParseTopic(topic)
			assert.False(t, ok, topic)
		}
	})
}

func TestValidID(t *testing.T) {
	t.Run("accepts env- and eight lowercase hex digits", func(t *testing.T) {
		assert.True(t, ValidID("env-1a2b3c4d"))
	})
	t.Run("refuses other ids", func(t *testing.T) {
		for _, id := range []string{"", "env-1A2B3C4D", "env-1a2b3c4", "env-1a2b3c4de", "+", "env-XYZ", "x env-1a2b3c4d"} {
			assert.False(t, ValidID(id), id)
		}
	})
}

func TestMatch(t *testing.T) {
	t.Run("plus matches one level", func(t *testing.T) {
		assert.True(t, Match("ws/v1/env/+/status", "ws/v1/env/env-1a2b3c4d/status"))
		assert.False(t, Match("ws/v1/env/+/status", "ws/v1/env/env-1a2b3c4d/snapshot"))
		assert.False(t, Match("a/+/c", "a/b/d/c"))
	})
	t.Run("hash matches zero or more trailing levels", func(t *testing.T) {
		assert.True(t, Match("ws/v1/#", "ws/v1/env/env-1a2b3c4d/status"))
		assert.True(t, Match("ws/v1/#", "ws/v1"))
		assert.True(t, Match("#", "anything/at/all"))
		assert.False(t, Match("ws/v1/#", "ws/v2/env"))
	})
	t.Run("literal levels must be equal", func(t *testing.T) {
		assert.True(t, Match("a/b", "a/b"))
		assert.False(t, Match("a/b", "a/b/c"))
		assert.False(t, Match("a/b/c", "a/b"))
	})
	t.Run("hash not in the last level matches nothing", func(t *testing.T) {
		assert.False(t, Match("a/#/c", "a/b/c"))
	})
}
