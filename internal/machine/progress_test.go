package machine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/workspace"
)

func TestProgress_Check(t *testing.T) {
	t.Run("accepts steps and short text", func(t *testing.T) {
		p := Progress{Name: "build auth", Total: 5, Done: 2, Note: "writing tests"}
		assert.NoError(t, p.Check())
	})
	t.Run("accepts the edges of every rule", func(t *testing.T) {
		assert.NoError(t, (&Progress{Total: 3, Done: 3}).Check())
		assert.NoError(t, (&Progress{Total: MaxProgressStep, Done: MaxProgressStep}).Check())
		assert.NoError(t, (&Progress{}).Check())
		assert.NoError(t, (&Progress{Name: strings.Repeat("x", MaxProgressName)}).Check())
		assert.NoError(t, (&Progress{Note: strings.Repeat("x", MaxProgressNote)}).Check())
	})
	t.Run("refuses done above total", func(t *testing.T) {
		err := (&Progress{Total: 2, Done: 3}).Check()
		require.ErrorIs(t, err, ErrProgressInvalid)
		require.ErrorContains(t, err, "more than total")
	})
	t.Run("refuses negative and oversized steps", func(t *testing.T) {
		err := (&Progress{Total: -1}).Check()
		require.ErrorIs(t, err, ErrProgressInvalid)
		require.ErrorContains(t, err, "total -1")
		err = (&Progress{Total: MaxProgressStep + 1}).Check()
		require.ErrorIs(t, err, ErrProgressInvalid)
		require.ErrorContains(t, err, "must be 0 to")
		err = (&Progress{Total: 1, Done: -1}).Check()
		require.ErrorIs(t, err, ErrProgressInvalid)
		require.ErrorContains(t, err, "negative")
	})
	t.Run("refuses a name or note over its length", func(t *testing.T) {
		err := (&Progress{Name: strings.Repeat("x", MaxProgressName+1)}).Check()
		require.ErrorIs(t, err, ErrProgressInvalid)
		require.ErrorContains(t, err, "name is over")
		err = (&Progress{Note: strings.Repeat("x", MaxProgressNote+1)}).Check()
		require.ErrorIs(t, err, ErrProgressInvalid)
		require.ErrorContains(t, err, "note is over")
	})
	t.Run("refuses a multi-line name and note", func(t *testing.T) {
		err := (&Progress{Name: "a\nb"}).Check()
		require.ErrorIs(t, err, ErrProgressInvalid)
		require.ErrorContains(t, err, "name must be one line")
		err = (&Progress{Note: "a\tb"}).Check()
		require.ErrorIs(t, err, ErrProgressInvalid)
		require.ErrorContains(t, err, "note must be one line")
	})
}

func TestStore_WriteProgress(t *testing.T) {
	t.Run("round-trips a record", func(t *testing.T) {
		store := NewStore(t.TempDir())
		when := testTime()
		require.NoError(t, store.WriteProgress("boss", "plan", &Progress{Name: "plan", Total: 4, Done: 1, Note: "n", UpdatedAt: when}))
		got, err := store.ReadProgress("boss", "plan")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, Progress{Version: FormatVersion, Name: "plan", Total: 4, Done: 1, Note: "n", UpdatedAt: when}, *got)
	})
	t.Run("a missing record reads as nil", func(t *testing.T) {
		got, err := NewStore(t.TempDir()).ReadProgress("boss", "plan")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
	t.Run("refuses an id that is a path", func(t *testing.T) {
		_, err := NewStore(t.TempDir()).ReadProgress("boss", "../x")
		assert.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("refuses a hand-edited record that fails Check, naming the path", func(t *testing.T) {
		for name, body := range map[string]string{
			"control characters": "version = 1\nname = \"a\\u0007b\"\n",
			"a huge note":        "version = 1\nnote = \"" + strings.Repeat("x", MaxProgressNote+1) + "\"\n",
			"negative steps":     "version = 1\ntotal = -3\n",
			"overflowing steps":  "version = 1\ntotal = 99999999999999999999\n",
			"done above total":   "version = 1\ntotal = 1\ndone = 2\n",
		} {
			t.Run(name, func(t *testing.T) {
				store := NewStore(t.TempDir())
				require.NoError(t, store.WriteProgress("boss", "plan", &Progress{Total: 1}))
				path := filepath.Join(store.Dir("boss", "plan"), progressFile)
				require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
				got, err := store.ReadProgress("boss", "plan")
				assert.Nil(t, got)
				require.ErrorIs(t, err, ErrInvalid)
				assert.ErrorContains(t, err, path)
			})
		}
	})
}

func TestStore_ReadProgressOf(t *testing.T) {
	t.Run("leaves out ids with no record", func(t *testing.T) {
		store := NewStore(t.TempDir())
		require.NoError(t, store.WriteProgress("boss", "a", &Progress{Total: 1}))
		got, err := store.ReadProgressOf("boss", []string{"a", "b"})
		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Contains(t, got, "a")
	})
}

func progressList() []workspace.Workspace {
	return []workspace.Workspace{
		{ID: "boss"},
		{ID: "plan", Parent: "boss"},
		{ID: "a", Parent: "plan"},
		{ID: "b", Parent: "plan"},
		{ID: "a1", Parent: "a"},
		{ID: "other", Parent: "boss"},
	}
}

func TestProgressTree(t *testing.T) {
	t.Run("sums own steps and descendants' up the tree", func(t *testing.T) {
		tree := ProgressTree(progressList(), "plan", map[string]*Progress{
			"plan":  {Total: 4, Done: 1},
			"a":     {Total: 3, Done: 3},
			"a1":    {Total: 2, Done: 1},
			"other": {Total: 9, Done: 9},
		}, nil)
		assert.Equal(t, Rollup{Total: 9, Done: 5}, tree.Rollup)
		require.Len(t, tree.Children, 2)
		assert.Equal(t, "a", tree.Children[0].ID)
		assert.Equal(t, Rollup{Total: 5, Done: 4}, tree.Children[0].Rollup)
	})
	t.Run("a workspace with no record passes its children's steps up", func(t *testing.T) {
		tree := ProgressTree(progressList(), "plan", map[string]*Progress{"a1": {Total: 2, Done: 2}}, nil)
		assert.Nil(t, tree.Progress)
		assert.Equal(t, Rollup{Total: 2, Done: 2}, tree.Rollup)
	})
	t.Run("a finished child with no record counts as one step, done only if it passed", func(t *testing.T) {
		tree := ProgressTree(progressList(), "plan", nil, map[string]Outcome{"a": OutcomePass, "b": OutcomeFail})
		assert.Equal(t, Rollup{Total: 2, Done: 1}, tree.Rollup)
	})
	t.Run("a declared record wins over the implicit step", func(t *testing.T) {
		tree := ProgressTree(progressList(), "plan", map[string]*Progress{"a": {Total: 3, Done: 1}}, map[string]Outcome{"a": OutcomePass})
		assert.Equal(t, Rollup{Total: 3, Done: 1}, tree.Rollup)
	})
	t.Run("ends at a cycle in parents", func(t *testing.T) {
		list := []workspace.Workspace{{ID: "x", Parent: "y"}, {ID: "y", Parent: "x"}}
		tree := ProgressTree(list, "x", nil, nil)
		assert.Equal(t, []string{"x", "y"}, tree.IDs())
	})
}

func TestRollup_Fraction(t *testing.T) {
	t.Run("is false with no steps", func(t *testing.T) {
		_, ok := Rollup{}.Fraction()
		assert.False(t, ok)
	})
	t.Run("is done over total", func(t *testing.T) {
		f, ok := Rollup{Total: 4, Done: 1}.Fraction()
		assert.True(t, ok)
		assert.InDelta(t, 0.25, f, 0.0001)
	})
}

func TestProgress_Complete(t *testing.T) {
	t.Run("finishes the declared steps", func(t *testing.T) {
		p := Progress{Total: 4, Done: 3}
		assert.True(t, p.Complete())
		assert.Equal(t, 4, p.Done)
	})
	t.Run("leaves a record with no steps or all done alone", func(t *testing.T) {
		assert.False(t, (&Progress{}).Complete())
		assert.False(t, (&Progress{Total: 2, Done: 2}).Complete())
	})
}

func TestProgressNode_AddRemote(t *testing.T) {
	t.Run("adds to the parent and every ancestor", func(t *testing.T) {
		root := &ProgressNode{ID: "plan", Rollup: Rollup{Total: 3, Done: 1}, Children: []*ProgressNode{
			{ID: "a", Rollup: Rollup{Total: 2, Done: 1}, Children: []*ProgressNode{
				{ID: "a1", Rollup: Rollup{Total: 1}},
			}},
		}}
		require.True(t, root.AddRemote("a1", Rollup{Total: 2, Done: 1}))
		assert.Equal(t, Rollup{Total: 5, Done: 2}, root.Rollup)
		assert.Equal(t, Rollup{Total: 4, Done: 2}, root.Children[0].Rollup)
		assert.Equal(t, Rollup{Total: 3, Done: 1}, root.Children[0].Children[0].Rollup)
	})
	t.Run("reports false for an unknown parent", func(t *testing.T) {
		root := &ProgressNode{ID: "plan", Rollup: Rollup{Total: 3, Done: 1}}
		assert.False(t, root.AddRemote("zz", Rollup{Total: 2, Done: 1}))
		assert.Equal(t, Rollup{Total: 3, Done: 1}, root.Rollup)
	})
	t.Run("leaves other branches alone", func(t *testing.T) {
		root := &ProgressNode{ID: "plan", Children: []*ProgressNode{
			{ID: "a"},
			{ID: "b"},
		}}
		require.True(t, root.AddRemote("a", Rollup{Total: 2, Done: 1}))
		assert.Equal(t, Rollup{Total: 2, Done: 1}, root.Rollup)
		assert.Equal(t, Rollup{Total: 2, Done: 1}, root.Children[0].Rollup)
		assert.Equal(t, Rollup{}, root.Children[1].Rollup)
	})
}
