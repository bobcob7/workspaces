package artifact

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCounts(t *testing.T) {
	t.Run("counts open, resolved and detached threads", func(t *testing.T) {
		threads := []Thread{
			{ID: "t1"},
			{ID: "t2", Resolved: true},
			{ID: "t3", Anchor: &Anchor{State: "detached"}},
			{ID: "t4", Resolved: true, Anchor: &Anchor{State: "detached"}},
			{ID: "t5", Anchor: &Anchor{State: "anchored"}},
		}
		assert.Equal(t, ThreadCounts{Open: 3, Resolved: 2, Detached: 2}, Counts(threads))
	})
	t.Run("is zero for no threads", func(t *testing.T) {
		assert.Equal(t, ThreadCounts{}, Counts(nil))
	})
}

func TestRecord_Latest(t *testing.T) {
	t.Run("returns the last revision", func(t *testing.T) {
		r := Record{Revisions: []Revision{{Rev: 1}, {Rev: 2, Size: 7}}}
		assert.Equal(t, Revision{Rev: 2, Size: 7}, r.Latest())
	})
}

func TestConflictError_Error(t *testing.T) {
	t.Run("names the latest and the base revision", func(t *testing.T) {
		err := &ConflictError{Base: 1, Latest: 3}
		assert.Equal(t, "the latest revision is r3, not r1", err.Error())
	})
	t.Run("matches ErrConflict through errors wrapping", func(t *testing.T) {
		err := error(&ConflictError{Base: 1, Latest: 2})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrConflict)
		assert.NotErrorIs(t, err, ErrNotFound)
	})
}

func TestPerson(t *testing.T) {
	t.Run("is a person with no workspace or name", func(t *testing.T) {
		assert.Equal(t, Actor{Kind: "person"}, Person())
	})
}

func TestAgent(t *testing.T) {
	t.Run("records the workspace and name", func(t *testing.T) {
		actor := Agent("pitch-ab12cd34", "pitch")
		require.NotNil(t, actor.Workspace)
		require.NotNil(t, actor.Name)
		assert.Equal(t, "agent", actor.Kind)
		assert.Equal(t, "pitch-ab12cd34", *actor.Workspace)
		assert.Equal(t, "pitch", *actor.Name)
	})
}
