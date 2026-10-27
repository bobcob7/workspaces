package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewID(t *testing.T) {
	t.Run("lowercases", func(t *testing.T) {
		id, err := NewID("GitHub.com", "BobCob7", "Buff-Bear")
		require.NoError(t, err)
		assert.Equal(t, ID{Host: "github.com", Owner: "bobcob7", Name: "buff-bear"}, id)
	})
	t.Run("accepts nested owner groups", func(t *testing.T) {
		id, err := NewID("gitlab.com", "gitlab-org/api", "client-go")
		require.NoError(t, err)
		assert.Equal(t, "gitlab-org/api", id.Owner)
	})
	t.Run("accepts a port", func(t *testing.T) {
		id, err := NewID("h.com:2222", "o", "n")
		require.NoError(t, err)
		assert.Equal(t, "h.com:2222", id.Host)
	})
	t.Run("rejects a bad host", func(t *testing.T) {
		_, err := NewID("git hub.com", "o", "n")
		assert.ErrorIs(t, err, ErrInvalidSpec)
	})
	t.Run("rejects a bad owner", func(t *testing.T) {
		_, err := NewID("github.com", "a//b", "n")
		assert.ErrorIs(t, err, ErrInvalidSpec)
	})
	t.Run("rejects a bad name", func(t *testing.T) {
		_, err := NewID("github.com", "o", "n@x")
		assert.ErrorIs(t, err, ErrInvalidSpec)
	})
}

func TestID_String(t *testing.T) {
	id := ID{Host: "gitlab.com", Owner: "a/b", Name: "c"}
	assert.Equal(t, "gitlab.com/a/b/c", id.String())
}

func TestParseID(t *testing.T) {
	t.Run("parses what String writes", func(t *testing.T) {
		for _, want := range []ID{
			{Host: "github.com", Owner: "bobcob7", Name: "buff-bear"},
			{Host: "gitlab.com:2222", Owner: "a/b", Name: "c"},
		} {
			got, err := ParseID(want.String())
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
	})
	t.Run("lowercases", func(t *testing.T) {
		got, err := ParseID("GitHub.com/BobCob7/X")
		require.NoError(t, err)
		assert.Equal(t, "github.com/bobcob7/x", got.String())
	})
	t.Run("rejects fewer than three parts", func(t *testing.T) {
		for _, s := range []string{"", "github.com", "github.com/x"} {
			_, err := ParseID(s)
			require.ErrorIs(t, err, ErrInvalidSpec, s)
		}
	})
	t.Run("rejects an invalid part", func(t *testing.T) {
		_, err := ParseID("github.com/a b/c")
		require.ErrorIs(t, err, ErrInvalidSpec)
	})
}

func TestID_UpstreamURL(t *testing.T) {
	id := ID{Host: "github.com", Owner: "bobcob7", Name: "buff-bear"}
	assert.Equal(t, "https://github.com/bobcob7/buff-bear.git", id.UpstreamURL())
}

func TestValidateHostPort(t *testing.T) {
	t.Run("host only", func(t *testing.T) {
		assert.NoError(t, validateHostPort("h.com"))
	})
	t.Run("host and port", func(t *testing.T) {
		assert.NoError(t, validateHostPort("h.com:2222"))
	})
	t.Run("rejects a bad host", func(t *testing.T) {
		assert.Error(t, validateHostPort("h_x.com:22"))
	})
	t.Run("rejects a non-numeric port", func(t *testing.T) {
		assert.ErrorContains(t, validateHostPort("h.com:ssh"), "1 to 65535")
	})
	t.Run("rejects port 0", func(t *testing.T) {
		assert.Error(t, validateHostPort("h.com:0"))
	})
	t.Run("rejects a port above 65535", func(t *testing.T) {
		assert.Error(t, validateHostPort("h.com:65536"))
	})
	t.Run("rejects a leading zero", func(t *testing.T) {
		assert.Error(t, validateHostPort("h.com:022"))
	})
}

func TestValidateHost(t *testing.T) {
	t.Run("accepts a host name", func(t *testing.T) {
		assert.NoError(t, validateHost("gitlab.example-1.com"))
	})
	t.Run("rejects empty", func(t *testing.T) {
		assert.Error(t, validateHost(""))
	})
	t.Run("rejects a port", func(t *testing.T) {
		assert.Error(t, validateHost("host:22"))
	})
	t.Run("rejects underscores", func(t *testing.T) {
		assert.Error(t, validateHost("my_host.com"))
	})
}

func TestValidatePath(t *testing.T) {
	t.Run("accepts one segment", func(t *testing.T) {
		assert.NoError(t, validatePath("bobcob7"))
	})
	t.Run("accepts nested groups", func(t *testing.T) {
		assert.NoError(t, validatePath("a/b/c"))
	})
	t.Run("rejects an empty segment", func(t *testing.T) {
		assert.Error(t, validatePath("a//b"))
	})
	t.Run("rejects empty", func(t *testing.T) {
		assert.Error(t, validatePath(""))
	})
}

func TestValidateSegment(t *testing.T) {
	t.Run("accepts forge characters", func(t *testing.T) {
		assert.NoError(t, validateSegment("Buff_bear.rs-2"))
	})
	t.Run("rejects empty", func(t *testing.T) {
		assert.Error(t, validateSegment(""))
	})
	t.Run("rejects dot", func(t *testing.T) {
		assert.Error(t, validateSegment("."))
	})
	t.Run("rejects dot-dot", func(t *testing.T) {
		assert.Error(t, validateSegment(".."))
	})
	t.Run("rejects @", func(t *testing.T) {
		assert.Error(t, validateSegment("a@b"))
	})
	t.Run("rejects non-ASCII", func(t *testing.T) {
		assert.Error(t, validateSegment("café"))
	})
}

func TestOnlyChars(t *testing.T) {
	t.Run("letters and digits", func(t *testing.T) {
		assert.True(t, onlyChars("aZ09", ""))
	})
	t.Run("allowed extras", func(t *testing.T) {
		assert.True(t, onlyChars("a-b.c", ".-"))
	})
	t.Run("other characters", func(t *testing.T) {
		assert.False(t, onlyChars("a/b", ".-"))
	})
	t.Run("empty", func(t *testing.T) {
		assert.True(t, onlyChars("", ""))
	})
}
