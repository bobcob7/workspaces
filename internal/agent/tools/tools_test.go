package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		Dir:      dir,
		Root:     dir,
		Branch:   "feat/x",
		Verdicts: []string{"PASS", "FAIL"},
		Recorder: &recorderMock{RecordFunc: func(context.Context, string, []byte) error { return nil }},
		Shell:    &shellRunnerMock{},
	}
}

func toolByName(t *testing.T, tools []Tool, name string) Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	require.FailNow(t, "no tool named "+name)
	return Tool{}
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("returns the seven tools in order", func(t *testing.T) {
		t.Parallel()
		var names []string
		for _, tool := range New(testConfig(t)) {
			names = append(names, tool.Name)
		}
		assert.Equal(t, []string{"read_file", "list_files", "search", "write_file", "edit_file", "run", "record_result"}, names)
	})
	t.Run("cuts results to MaxResult", func(t *testing.T) {
		t.Parallel()
		long := func(context.Context, string) (Result, error) {
			return Result{Text: strings.Repeat("a", MaxResult+100)}, nil
		}
		res, err := capped(long)(t.Context(), "{}")
		require.NoError(t, err)
		assert.True(t, res.Truncated)
		assert.True(t, strings.HasSuffix(res.Text, "[truncated: 100 more bytes]"))
	})
}

func TestValidate(t *testing.T) {
	schema := objectSchema(map[string]map[string]any{
		"path":  field("string", ""),
		"limit": field("integer", ""),
		"all":   field("boolean", ""),
	}, "path")

	tests := []struct {
		name      string
		arguments string
		wantErr   string
	}{
		{name: "accepts matching arguments", arguments: `{"path":"a","limit":3,"all":true}`},
		{name: "rejects invalid JSON", arguments: `{"path":`, wantErr: "not a JSON object"},
		{name: "rejects a missing required field", arguments: `{"limit":3}`, wantErr: "missing path"},
		{name: "rejects a string where an integer is due", arguments: `{"path":"a","limit":"3"}`, wantErr: "limit must be of type integer"},
		{name: "rejects a number where a boolean is due", arguments: `{"path":"a","all":1}`, wantErr: "all must be of type boolean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(schema, tt.arguments)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestCapText(t *testing.T) {
	t.Run("keeps short text", func(t *testing.T) {
		text, cut := capText("short")
		assert.Equal(t, "short", text)
		assert.False(t, cut)
	})
	t.Run("cuts long text with the byte count", func(t *testing.T) {
		text, cut := capText(strings.Repeat("a", MaxResult+5))
		assert.True(t, cut)
		assert.True(t, strings.HasSuffix(text, "[truncated: 5 more bytes]"))
	})
}
