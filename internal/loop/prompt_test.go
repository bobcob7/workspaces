package loop

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderPrompt(t *testing.T) {
	t.Run("substitutes name, vars, and script output", func(t *testing.T) {
		l := Loop{
			Version: FormatVersion,
			Name:    "deps",
			Prompt:  "Fix {{.Stdout}} ({{.Stderr}}) in {{.Vars.REPO}} for {{.Name}}",
			Script:  "true",
			Vars:    map[string]string{"REPO": "buff-bear"},
		}
		got, err := l.RenderPrompt(PromptData{Stdout: "#1", Stderr: "warn"})
		require.NoError(t, err)
		assert.Equal(t, "Fix #1 (warn) in buff-bear for deps", got)
	})
	t.Run("escapes a literal open brace", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: `{{"{{"}}`}
		got, err := l.RenderPrompt(PromptData{})
		require.NoError(t, err)
		assert.Equal(t, "{{", got)
	})
	t.Run("version 1 prompt is literal", func(t *testing.T) {
		l := Loop{Version: Version1, Name: "old", Prompt: "literal {{ .Stdout }}"}
		got, err := l.RenderPrompt(PromptData{Stdout: "ignored"})
		require.NoError(t, err)
		assert.Equal(t, "literal {{ .Stdout }}", got)
	})
	t.Run("missing var with nil vars is invalid", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: "x{{.Vars.NOPE}}"}
		_, err := l.RenderPrompt(PromptData{})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("missing key in a branch is invalid", func(t *testing.T) {
		l := Loop{
			Version: FormatVersion,
			Name:    "deps",
			Prompt:  "{{if .Stdout}}{{.Vars.NOPE}}{{end}}x",
			Script:  "true",
		}
		_, err := l.RenderPrompt(PromptData{Stdout: "y"})
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("blank result is invalid", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: "{{.Stdout}}", Script: "true"}
		_, err := l.RenderPrompt(PromptData{})
		require.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "renders empty")
	})
}

func TestCheckPrompt(t *testing.T) {
	t.Run("blank result before the script prints is ok", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: "{{.Stdout}}", Script: "true"}
		assert.NoError(t, l.CheckPrompt())
	})
	t.Run("parse error is invalid", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: "{{"}
		assert.ErrorIs(t, l.CheckPrompt(), ErrInvalid)
	})
	t.Run("missing key is invalid", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: "{{.Stdout}}"}
		assert.ErrorIs(t, l.CheckPrompt(), ErrInvalid)
	})
	t.Run("memory function is ok", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: `seen {{memory "seen"}}`}
		assert.NoError(t, l.CheckPrompt())
	})
}

func TestLoop_RenderPrompt(t *testing.T) {
	t.Run("memory function renders a set key's value", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: `seen: {{memory "seen"}}`}
		got, err := l.RenderPrompt(PromptData{Memory: map[string]string{"seen": "#412 #415"}})
		require.NoError(t, err)
		assert.Equal(t, "seen: #412 #415", got)
	})
	t.Run("memory function renders an unset key as empty", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: `seen: [{{memory "seen"}}] {{.Name}}`}
		got, err := l.RenderPrompt(PromptData{Memory: map[string]string{"other": "x"}})
		require.NoError(t, err)
		assert.Equal(t, "seen: [] deps", got)
	})
	t.Run("memory function with a nil map renders empty", func(t *testing.T) {
		l := Loop{Version: FormatVersion, Name: "deps", Prompt: `[{{memory "seen"}}] {{.Name}}`}
		got, err := l.RenderPrompt(PromptData{})
		require.NoError(t, err)
		assert.Equal(t, "[] deps", got)
	})
	t.Run("version 1 prompt ignores memory", func(t *testing.T) {
		l := Loop{Version: Version1, Name: "old", Prompt: `literal {{memory "seen"}}`}
		got, err := l.RenderPrompt(PromptData{Memory: map[string]string{"seen": "x"}})
		require.NoError(t, err)
		assert.Equal(t, `literal {{memory "seen"}}`, got)
	})
}
