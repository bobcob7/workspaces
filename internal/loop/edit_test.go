package loop

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var editNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func editLoop() Loop {
	l := validLoop()
	l.Name = "deps-bump"
	l.CreatedAt = editNow.Add(-72 * time.Hour)
	l.NextRunAt = editNow.Add(time.Hour)
	return l
}

func TestEdit_Empty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		edit  Edit
		empty bool
	}{
		{name: "zero", edit: Edit{}, empty: true},
		{name: "title", edit: Edit{Title: new("")}},
		{name: "every", edit: Edit{Every: new("1h")}},
		{name: "prompt", edit: Edit{Prompt: new("x")}},
		{name: "specs", edit: Edit{Specs: &[]string{}}},
		{name: "keep", edit: Edit{Keep: new(1)}},
		{name: "model", edit: Edit{Model: new("")}},
		{name: "permission mode", edit: Edit{PermissionMode: new("")}},
		{name: "script", edit: Edit{Script: new("")}},
		{name: "script timeout", edit: Edit{ScriptTimeout: new("1m")}},
		{name: "vars", edit: Edit{Vars: &map[string]string{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.empty, tt.edit.Empty())
		})
	}
}

func TestLoop_Apply(t *testing.T) {
	t.Parallel()

	t.Run("every counts from the last run", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Every = "24h"
		l.AddRun(Run{StartedAt: editNow.Add(-2 * time.Hour)})
		require.NoError(t, l.Apply(&Edit{Every: new("6h")}, editNow))
		assert.Equal(t, "6h", l.Every)
		assert.Equal(t, editNow.Add(4*time.Hour), l.NextRunAt)
	})

	t.Run("every past the last run counts from now", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Every = "6h"
		l.AddRun(Run{StartedAt: editNow.Add(-2 * time.Hour)})
		require.NoError(t, l.Apply(&Edit{Every: new("1h")}, editNow))
		assert.Equal(t, editNow.Add(time.Hour), l.NextRunAt)
	})

	t.Run("every without runs counts from creation", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Every = "24h"
		l.CreatedAt = editNow.Add(-30 * time.Minute)
		require.NoError(t, l.Apply(&Edit{Every: new("2h")}, editNow))
		assert.Equal(t, editNow.Add(90*time.Minute), l.NextRunAt)
	})

	t.Run("same length keeps next run", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Every = "1h"
		next := editNow.Add(7 * time.Minute)
		l.NextRunAt = next
		require.NoError(t, l.Apply(&Edit{Every: new("60m")}, editNow))
		assert.Equal(t, "1h", l.Every)
		assert.Equal(t, next, l.NextRunAt)
	})

	t.Run("every is saved in canonical form", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Every = "1h"
		require.NoError(t, l.Apply(&Edit{Every: new("90m")}, editNow))
		assert.Equal(t, "1h30m", l.Every)
	})

	t.Run("rejected edits leave the loop unchanged", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			edit Edit
		}{
			{name: "bad title", edit: Edit{Title: new("two\nlines")}},
			{name: "bad every", edit: Edit{Every: new("2m")}},
			{name: "blank prompt", edit: Edit{Prompt: new(" ")}},
			{name: "keep zero", edit: Edit{Keep: new(0)}},
			{name: "keep too big", edit: Edit{Keep: new(MaxKeep + 1)}},
			{name: "bad var key", edit: Edit{Vars: &map[string]string{"a-b": "x"}}},
			{name: "prompt uses missing var", edit: Edit{Prompt: new("{{.Vars.X}}")}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := editLoop()
				before := l
				err := l.Apply(&tt.edit, editNow)
				require.ErrorIs(t, err, ErrInvalid)
				assert.Equal(t, before, l)
			})
		}
	})

	t.Run("specs are replaced", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Specs = []string{"repo-a"}
		require.NoError(t, l.Apply(&Edit{Specs: &[]string{"repo-b", "repo-c"}}, editNow))
		assert.Equal(t, []string{"repo-b", "repo-c"}, l.Specs)
	})

	t.Run("empty specs are nil", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Specs = []string{"repo-a"}
		require.NoError(t, l.Apply(&Edit{Specs: &[]string{}}, editNow))
		assert.Nil(t, l.Specs)
	})

	t.Run("title is set trimmed", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		require.NoError(t, l.Apply(&Edit{Title: new("  Nightly deps bump ")}, editNow))
		assert.Equal(t, "Nightly deps bump", l.Title)
	})

	t.Run("blank title clears it", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Title = "Nightly deps bump"
		require.NoError(t, l.Apply(&Edit{Title: new("")}, editNow))
		assert.Empty(t, l.Title)
	})

	t.Run("blank model clears it", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Model = "sonnet"
		require.NoError(t, l.Apply(&Edit{Model: new("")}, editNow))
		assert.Empty(t, l.Model)
	})

	t.Run("model and permission mode are trimmed", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		require.NoError(t, l.Apply(&Edit{Model: new(" opus "), PermissionMode: new(" plan ")}, editNow))
		assert.Equal(t, "opus", l.Model)
		assert.Equal(t, "plan", l.PermissionMode)
	})

	t.Run("script on a loop without one gets the default timeout", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		require.NoError(t, l.Apply(&Edit{Script: new("make check")}, editNow))
		assert.Equal(t, "make check", l.Script)
		assert.Equal(t, "1m", l.ScriptTimeout)
	})

	t.Run("script with a timeout keeps the timeout", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		edit := Edit{Script: new("make check"), ScriptTimeout: new("45s")}
		require.NoError(t, l.Apply(&edit, editNow))
		assert.Equal(t, "45s", l.ScriptTimeout)
	})

	t.Run("changing the script clears the gate", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Script = "old"
		l.ScriptTimeout = "1m"
		l.LastGate = &Gate{Outcome: GatePassed}
		require.NoError(t, l.Apply(&Edit{Script: new("new")}, editNow))
		assert.Nil(t, l.LastGate)
	})

	t.Run("the same script keeps the gate", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Script = "old"
		l.ScriptTimeout = "1m"
		gate := &Gate{Outcome: GatePassed}
		l.LastGate = gate
		require.NoError(t, l.Apply(&Edit{Script: new("old")}, editNow))
		assert.Equal(t, gate, l.LastGate)
	})

	t.Run("blank script clears script, timeout, and gate", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Script = "old"
		l.ScriptTimeout = "2m"
		l.LastGate = &Gate{Outcome: GatePassed}
		require.NoError(t, l.Apply(&Edit{Script: new("  ")}, editNow))
		assert.Empty(t, l.Script)
		assert.Empty(t, l.ScriptTimeout)
		assert.Nil(t, l.LastGate)
	})

	t.Run("script timeout without a script is invalid", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		before := l
		err := l.Apply(&Edit{ScriptTimeout: new("1m")}, editNow)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Equal(t, before, l)
	})

	t.Run("script timeout is saved in canonical form", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Script = "make check"
		l.ScriptTimeout = "1m"
		require.NoError(t, l.Apply(&Edit{ScriptTimeout: new("90s")}, editNow))
		assert.Equal(t, "1m30s", l.ScriptTimeout)
	})

	t.Run("vars are replaced", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Vars = map[string]string{"A": "1"}
		require.NoError(t, l.Apply(&Edit{Vars: &map[string]string{"B": "2"}}, editNow))
		assert.Equal(t, map[string]string{"B": "2"}, l.Vars)
	})

	t.Run("empty vars are nil", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Vars = map[string]string{"A": "1"}
		require.NoError(t, l.Apply(&Edit{Vars: &map[string]string{}}, editNow))
		assert.Nil(t, l.Vars)
	})

	t.Run("removing a var the prompt uses is invalid", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Vars = map[string]string{"A": "1"}
		l.Prompt = "bump {{.Vars.A}}"
		before := l
		err := l.Apply(&Edit{Vars: &map[string]string{"B": "2"}}, editNow)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Equal(t, before, l)
	})

	t.Run("removing a var with a prompt that no longer uses it", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Vars = map[string]string{"A": "1"}
		l.Prompt = "bump {{.Vars.A}}"
		edit := Edit{Vars: &map[string]string{"B": "2"}, Prompt: new("run {{.Vars.B}}")}
		require.NoError(t, l.Apply(&edit, editNow))
		assert.Equal(t, map[string]string{"B": "2"}, l.Vars)
		assert.Equal(t, "run {{.Vars.B}}", l.Prompt)
	})

	t.Run("every-only edit keeps version 1", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Version = Version1
		require.NoError(t, l.Apply(&Edit{Every: new("2h")}, editNow))
		assert.Equal(t, Version1, l.Version)
	})

	t.Run("prompt, script, or vars edit makes version 1 current", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			edit Edit
		}{
			{name: "prompt", edit: Edit{Prompt: new("bump deps")}},
			{name: "script", edit: Edit{Script: new("true")}},
			{name: "vars", edit: Edit{Vars: &map[string]string{"A": "1"}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := editLoop()
				l.Version = Version1
				require.NoError(t, l.Apply(&tt.edit, editNow))
				assert.Equal(t, FormatVersion, l.Version)
			})
		}
	})

	t.Run("version 1 prompt with braces is invalid once vars are set", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Version = Version1
		l.Prompt = "use {{x}}"
		before := l
		err := l.Apply(&Edit{Vars: &map[string]string{"A": "1"}}, editNow)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Equal(t, before, l)
	})

	t.Run("full edit keeps runs, created_at, paused, and name", func(t *testing.T) {
		t.Parallel()
		l := editLoop()
		l.Paused = true
		l.AddRun(Run{StartedAt: editNow.Add(-time.Hour), Workspace: "deps-bump-00000001"})
		before := l
		edit := Edit{
			Every:          new("2h"),
			Prompt:         new("bump {{.Vars.A}}"),
			Specs:          &[]string{"repo-a"},
			Keep:           new(3),
			Model:          new("opus"),
			PermissionMode: new("plan"),
			Script:         new("make check"),
			ScriptTimeout:  new("30s"),
			Vars:           &map[string]string{"A": "1"},
		}
		require.NoError(t, l.Apply(&edit, editNow))
		assert.Equal(t, "2h", l.Every)
		assert.Equal(t, "bump {{.Vars.A}}", l.Prompt)
		assert.Equal(t, []string{"repo-a"}, l.Specs)
		assert.Equal(t, 3, l.Keep)
		assert.Equal(t, "opus", l.Model)
		assert.Equal(t, "plan", l.PermissionMode)
		assert.Equal(t, "make check", l.Script)
		assert.Equal(t, "30s", l.ScriptTimeout)
		assert.Equal(t, map[string]string{"A": "1"}, l.Vars)
		assert.Equal(t, before.Runs, l.Runs)
		assert.Equal(t, before.CreatedAt, l.CreatedAt)
		assert.Equal(t, before.Paused, l.Paused)
		assert.Equal(t, before.Name, l.Name)
	})
}
