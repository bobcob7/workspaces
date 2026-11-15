package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticToolset(t *testing.T) {
	ts := StaticToolset(fakeTools())
	t.Run("offers its tools unchanged", func(t *testing.T) {
		names := make([]string, 0)
		for _, def := range ts.Definitions() {
			names = append(names, def.Name)
		}
		assert.Equal(t, []string{"write_file", "run", "record_result"}, names)
	})
	t.Run("knows its tools only", func(t *testing.T) {
		assert.True(t, ts.Known("run"))
		assert.False(t, ts.Known("nope"))
	})
	t.Run("calls a tool by name and refuses an unknown one", func(t *testing.T) {
		res, err := ts.Call(t.Context(), "run", `{"command":"true"}`)
		require.NoError(t, err)
		assert.Equal(t, "exit 0", res.Text)
		_, err = ts.Call(context.Background(), "nope", "{}")
		assert.ErrorContains(t, err, `unknown tool "nope"`)
	})
}
