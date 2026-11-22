package machine

import (
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
	t.Run("refuses done above total", func(t *testing.T) {
		p := Progress{Total: 2, Done: 3}
		require.ErrorIs(t, p.Check(), ErrProgressInvalid)
	})
	t.Run("refuses negative and oversized steps", func(t *testing.T) {
		require.ErrorIs(t, (&Progress{Total: -1}).Check(), ErrProgressInvalid)
		require.ErrorIs(t, (&Progress{Total: MaxProgressStep + 1}).Check(), ErrProgressInvalid)
		require.ErrorIs(t, (&Progress{Total: 1, Done: -1}).Check(), ErrProgressInvalid)
	})
	t.Run("refuses a long or multi-line name and note", func(t *testing.T) {
		long := string(make([]rune, MaxProgressName+1))
		require.ErrorIs(t, (&Progress{Name: "x" + long}).Check(), ErrProgressInvalid)
		require.ErrorIs(t, (&Progress{Name: "a\nb"}).Check(), ErrProgressInvalid)
		require.ErrorIs(t, (&Progress{Note: "a\tb"}).Check(), ErrProgressInvalid)
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
