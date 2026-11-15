package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testGetenv(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestExpand(t *testing.T) {
	getenv := testGetenv(map[string]string{"A": "a-value", "EMPTY": ""})
	t.Run("expands braces in every field and returns the substituted values", func(t *testing.T) {
		def := ServerDef{
			Name:    "srv",
			Command: "${A}",
			Args:    []string{"x-${A}"},
			Env:     map[string]string{"K": "${A}"},
			URL:     "https://h/${A}",
			Headers: map[string]string{"Authorization": "Bearer ${A}"},
		}
		got, secrets, err := Expand(def, getenv)
		require.NoError(t, err)
		assert.Equal(t, "a-value", got.Command)
		assert.Equal(t, []string{"x-a-value"}, got.Args)
		assert.Equal(t, map[string]string{"K": "a-value"}, got.Env)
		assert.Equal(t, "https://h/a-value", got.URL)
		assert.Equal(t, map[string]string{"Authorization": "Bearer a-value"}, got.Headers)
		assert.Contains(t, secrets, "a-value")
		assert.Contains(t, secrets, "Bearer a-value")
		assert.Equal(t, "${A}", def.Env["K"], "the input map must not change")
	})
	t.Run("a default applies when unset or empty", func(t *testing.T) {
		got, _, err := Expand(ServerDef{Command: "${B:-x}", Args: []string{"${EMPTY:-d}"}}, getenv)
		require.NoError(t, err)
		assert.Equal(t, "x", got.Command)
		assert.Equal(t, []string{"d"}, got.Args)
	})
	t.Run("a dollar without braces is unchanged", func(t *testing.T) {
		got, _, err := Expand(ServerDef{Command: "$A"}, getenv)
		require.NoError(t, err)
		assert.Equal(t, "$A", got.Command)
	})
	t.Run("an unset variable without a default is UnsetVarError", func(t *testing.T) {
		_, _, err := Expand(ServerDef{Name: "srv", Command: "${C}"}, getenv)
		var unset *UnsetVarError
		require.ErrorAs(t, err, &unset)
		assert.Equal(t, "C", unset.Var)
		assert.Equal(t, "srv", unset.Server)
		assert.NotContains(t, err.Error(), "a-value")
	})
	t.Run("a literal header value is returned as a secret", func(t *testing.T) {
		_, secrets, err := Expand(ServerDef{Headers: map[string]string{"Authorization": "Bearer fake"}}, getenv)
		require.NoError(t, err)
		assert.Equal(t, []string{"Bearer fake"}, secrets)
	})
}
