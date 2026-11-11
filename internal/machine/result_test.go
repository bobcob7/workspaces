package machine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/workspace"
)

func TestNormalizeVerdict(t *testing.T) {
	t.Parallel()

	t.Run("upper-cases a valid verdict", func(t *testing.T) {
		t.Parallel()
		got, err := NormalizeVerdict("pass")
		require.NoError(t, err)
		assert.Equal(t, "PASS", got)
	})

	t.Run("keeps underscores", func(t *testing.T) {
		t.Parallel()
		got, err := NormalizeVerdict("CHANGES_REQUESTED")
		require.NoError(t, err)
		assert.Equal(t, "CHANGES_REQUESTED", got)
	})

	t.Run("accepts 32 characters", func(t *testing.T) {
		t.Parallel()
		got, err := NormalizeVerdict("a" + strings.Repeat("1", 31))
		require.NoError(t, err)
		assert.Len(t, got, 32)
	})

	invalid := map[string]string{
		"empty":           "",
		"leading digit":   "1PASS",
		"space":           "PA SS",
		"hyphen":          "PASS-1",
		"33 characters":   "A" + strings.Repeat("B", 32),
		"leading under":   "_PASS",
		"non-ASCII":       "PÄSS",
		"leading newline": "PASS\n",
	}
	for name, verdict := range invalid {
		t.Run("rejects "+name, func(t *testing.T) {
			t.Parallel()
			_, err := NormalizeVerdict(verdict)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestOutcomeOf(t *testing.T) {
	t.Parallel()

	t.Run("nil spawn uses the defaults", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, OutcomePass, OutcomeOf(nil, "PASS"))
		assert.Equal(t, OutcomePass, OutcomeOf(nil, "APPROVED"))
		assert.Equal(t, OutcomeFail, OutcomeOf(nil, "FAIL"))
		assert.Equal(t, OutcomeFail, OutcomeOf(nil, "CHANGES_REQUESTED"))
		assert.Equal(t, OutcomeFail, OutcomeOf(nil, "WHATEVER"))
	})

	t.Run("spawn's pass list decides", func(t *testing.T) {
		t.Parallel()
		sp := &Spawn{Pass: []string{"PASS", "FLAKY"}, Fail: []string{"FAIL"}}
		assert.Equal(t, OutcomePass, OutcomeOf(sp, "FLAKY"))
		assert.Equal(t, OutcomeFail, OutcomeOf(sp, "APPROVED"))
	})

	t.Run("BLOCKED with no spawn is blocked", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, OutcomeBlocked, OutcomeOf(nil, VerdictBlocked))
	})

	t.Run("BLOCKED a spawn doesn't list is fail", func(t *testing.T) {
		t.Parallel()
		sp := &Spawn{Pass: []string{"PASS"}, Fail: []string{"FAIL"}}
		assert.Equal(t, OutcomeFail, OutcomeOf(sp, VerdictBlocked))
	})
}

func TestSpawnAllows(t *testing.T) {
	t.Parallel()

	sp := &Spawn{Pass: []string{"PASS"}, Fail: []string{"FAIL"}}
	assert.True(t, sp.Allows("PASS"))
	assert.True(t, sp.Allows("FAIL"))
	assert.False(t, sp.Allows("APPROVED"))
}

func TestRecordMethods(t *testing.T) {
	t.Parallel()

	t.Run("empty record", func(t *testing.T) {
		t.Parallel()
		var r Record
		assert.False(t, r.Found())
		assert.Empty(t, r.Role())
		assert.Nil(t, r.Ancestors())
		_, ok := r.Outcome()
		assert.False(t, ok)
	})

	t.Run("result takes precedence for ancestors and outcome", func(t *testing.T) {
		t.Parallel()
		r := Record{
			Spawn:  &Spawn{Role: "implementer", Ancestors: []string{"old"}, Pass: []string{"DONE"}},
			Result: &Result{Verdict: "DONE", Ancestors: []string{"plan", "boss"}},
		}
		assert.True(t, r.Found())
		assert.Equal(t, "implementer", r.Role())
		assert.Equal(t, []string{"plan", "boss"}, r.Ancestors())
		outcome, ok := r.Outcome()
		require.True(t, ok)
		assert.Equal(t, OutcomePass, outcome)
	})

	t.Run("spawn alone supplies ancestors", func(t *testing.T) {
		t.Parallel()
		r := Record{Spawn: &Spawn{Ancestors: []string{"boss"}}}
		assert.Equal(t, []string{"boss"}, r.Ancestors())
	})
}

func TestAncestors(t *testing.T) {
	t.Parallel()

	list := []workspace.Workspace{
		{ID: "boss"},
		{ID: "plan", Parent: "boss"},
		{ID: "a", Parent: "plan"},
		{ID: "a1", Parent: "a"},
		{ID: "other", Parent: "boss"},
		{ID: "orphan", Parent: "missing"},
		{ID: "x", Parent: "y"},
		{ID: "y", Parent: "x"},
	}

	t.Run("walks up to the root", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"a", "plan", "boss"}, Ancestors(list, "a1"))
	})

	t.Run("a root has none", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, Ancestors(list, "boss"))
	})

	t.Run("a parent missing from the list stops the walk", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, Ancestors(list, "orphan"))
	})

	t.Run("a cycle terminates", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"y"}, Ancestors(list, "x"))
	})

	t.Run("an id not in the list has none", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, Ancestors(list, "nowhere"))
	})
}

func TestRoot(t *testing.T) {
	t.Parallel()

	list := []workspace.Workspace{
		{ID: "boss"},
		{ID: "plan", Parent: "boss"},
		{ID: "a", Parent: "plan"},
		{ID: "a1", Parent: "a"},
		{ID: "loose", Parent: "gone"},
	}

	assert.Equal(t, "boss", Root(list, "a1"))
	assert.Equal(t, "boss", Root(list, "boss"))
	assert.Equal(t, "loose", Root(list, "loose"))
	assert.Equal(t, "nowhere", Root(list, "nowhere"))
}
