package lifetime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/workspace"
)

// itemIDs is the workspace ids of items, in order.
func itemIDs(items []TreeItem) []string {
	var ids []string
	for i := range items {
		ids = append(ids, items[i].Workspace.ID)
	}
	return ids
}

// tree adds root's agent, its children a and b, and a's child a1, each
// with a listed session.
func (f *fixture) tree() workspace.Workspace {
	for _, id := range []string{"root", "a", "b", "a1"} {
		f.agent(id, id+"-sess")
		f.listed(id+"-sess", "/cfg/workspaces/"+id)
	}
	f.childOf("a", "root")
	f.childOf("b", "root")
	f.childOf("a1", "a")
	return f.workspaces[0]
}

func TestManager_RemoveTree(t *testing.T) {
	t.Run("removes descendants deepest first, then the workspace", func(t *testing.T) {
		f := newFixture(t)
		root := f.tree()
		items, err := f.m.RemoveTree(t.Context(), &root, TreeOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"a1", "a", "b", "root"}, itemIDs(items))
		for i := range items {
			require.NoError(t, items[i].Err)
		}
		assert.Equal(t, []string{"a1", "a", "b", "root"}, f.removedIDs())
		assert.Equal(t, []string{"a1-sess", "a-sess", "b-sess", "root-sess"}, f.claudeRemoved())
	})
	t.Run("a dry run removes nothing", func(t *testing.T) {
		f := newFixture(t)
		root := f.tree()
		items, err := f.m.RemoveTree(t.Context(), &root, TreeOptions{DryRun: true})
		require.NoError(t, err)
		assert.Len(t, items, 4)
		assert.Empty(t, f.removedIDs())
		assert.Empty(t, f.claudeRemoved())
	})
	t.Run("a child that can't go keeps its ancestors, not its siblings", func(t *testing.T) {
		f := newFixture(t)
		root := f.tree()
		f.shell("sh")
		f.childOf("sh", "a")
		f.locked["sh"] = true
		items, err := f.m.RemoveTree(t.Context(), &root, TreeOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"a1", "sh", "a", "b", "root"}, itemIDs(items))
		require.ErrorIs(t, items[1].Err, ErrShellRunning)
		require.ErrorIs(t, items[2].Err, ErrChildKept)
		require.ErrorIs(t, items[4].Err, ErrChildKept)
		assert.Equal(t, []string{"a1", "b"}, f.removedIDs())
	})
	t.Run("keeping children makes them roots", func(t *testing.T) {
		f := newFixture(t)
		root := f.tree()
		items, err := f.m.RemoveTree(t.Context(), &root, TreeOptions{KeepChildren: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"root"}, itemIDs(items))
		assert.Equal(t, []string{"root"}, f.removedIDs())
		assert.Empty(t, f.workspaces[1].Parent)
		assert.Empty(t, f.workspaces[2].Parent)
		assert.Equal(t, "a", f.workspaces[3].Parent, "grandchildren keep their parent")
	})
	t.Run("keeping children with a dry run changes nothing", func(t *testing.T) {
		f := newFixture(t)
		root := f.tree()
		_, err := f.m.RemoveTree(t.Context(), &root, TreeOptions{KeepChildren: true, DryRun: true})
		require.NoError(t, err)
		assert.Empty(t, f.store.ClearParentCalls())
		assert.Empty(t, f.removedIDs())
	})
	t.Run("a cycle is visited once", func(t *testing.T) {
		f := newFixture(t)
		f.shell("x")
		f.shell("y")
		f.childOf("y", "x")
		x := f.childOf("x", "y")
		items, err := f.m.RemoveTree(t.Context(), &x, TreeOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"y", "x"}, itemIDs(items))
	})
}

func TestManager_pass_cascade(t *testing.T) {
	t.Run("a gone parent takes its live descendants", func(t *testing.T) {
		f := newFixture(t)
		f.tree()
		f.unlist("root-sess")
		other := f.agent("other", "other-sess")
		f.listed("other-sess", "/cfg/workspaces/other")
		entries, removed, err := f.m.pass(t.Context(), newReader(f.m, false))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, other, entries[0].Workspace)
		assert.Len(t, removed, 4)
		assert.Equal(t, []string{"a1", "a", "b", "root"}, f.removedIDs())
		assert.Equal(t, []string{"a1-sess", "a-sess", "b-sess"}, f.claudeRemoved())
	})
	t.Run("a live parent keeps its children", func(t *testing.T) {
		f := newFixture(t)
		f.tree()
		entries, removed, err := f.m.pass(t.Context(), newReader(f.m, false))
		require.NoError(t, err)
		assert.Len(t, entries, 4)
		assert.Empty(t, removed)
	})
	t.Run("a child that can't go keeps its gone parent, with a warning", func(t *testing.T) {
		f := newFixture(t)
		f.agent("root", "root-sess")
		f.shell("sh")
		f.childOf("sh", "root")
		f.locked["sh"] = true
		entries, removed, err := f.m.pass(t.Context(), newReader(f.m, false))
		require.NoError(t, err)
		assert.Len(t, entries, 2)
		assert.Empty(t, removed)
		assert.Contains(t, f.m.Warnings(), "kept workspace root: kept because a workspace it started couldn't be removed: sh")
	})
	t.Run("a child whose parent is missing is a root", func(t *testing.T) {
		f := newFixture(t)
		f.agent("x", "x-sess")
		f.listed("x-sess", "/cfg/workspaces/x")
		f.childOf("x", "gone-long-ago")
		entries, _, err := f.m.pass(t.Context(), newReader(f.m, false))
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})
}
