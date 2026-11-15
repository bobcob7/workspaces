package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Logs(t *testing.T) {
	t.Parallel()
	t.Run("returns the log's text", func(t *testing.T) {
		t.Parallel()
		environ, args := argsFile(t)
		c := New(discardLogger(), fakeClaude(t, recordArgs+`; echo "log line"`), environ)
		out, err := c.Logs(t.Context(), "f381de70")
		require.NoError(t, err)
		assert.Equal(t, "log line\n", out)
		assert.Equal(t, []string{"logs", "f381de70"}, args())
	})
	t.Run("refuses a bad id without running claude", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "exit 1"), nil)
		_, err := c.Logs(t.Context(), "../x")
		require.ErrorIs(t, err, errBadID)
	})
}
