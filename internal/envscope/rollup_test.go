package envscope

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listedAt(env, ws, parent string, total, done int) Listed {
	return Listed{Environment: env, Workspace: ws, Parent: parent, Base: Steps{Total: total, Done: done}}
}

func ledgerChild(env, ws, parentEnv, parentWS string) Entry {
	return Entry{Environment: env, Workspace: ws, Parent: &Parent{Environment: parentEnv, Workspace: parentWS}}
}

func TestSteps_Add(t *testing.T) {
	t.Run("saturates at MaxRollupSteps and keeps done at most total", func(t *testing.T) {
		a := Steps{Total: MaxRollupSteps - 1, Done: MaxRollupSteps - 1}
		b := Steps{Total: 5, Done: 2}
		got := a.Add(b)
		assert.Equal(t, Steps{Total: MaxRollupSteps, Done: MaxRollupSteps}, got)
		assert.True(t, got.Valid())
	})
	t.Run("adds plain sums below the cap", func(t *testing.T) {
		assert.Equal(t, Steps{Total: 5, Done: 3}, Steps{Total: 3, Done: 1}.Add(Steps{Total: 2, Done: 2}))
	})
}

func TestSteps_Valid(t *testing.T) {
	tests := []struct {
		name  string
		steps Steps
		want  bool
	}{
		{name: "zero", steps: Steps{}, want: true},
		{name: "done equal to total", steps: Steps{Total: 4, Done: 4}, want: true},
		{name: "total at the cap", steps: Steps{Total: MaxRollupSteps, Done: MaxRollupSteps}, want: true},
		{name: "negative done", steps: Steps{Total: 3, Done: -1}, want: false},
		{name: "negative total", steps: Steps{Total: -1, Done: -1}, want: false},
		{name: "done above total", steps: Steps{Total: 2, Done: 3}, want: false},
		{name: "total above the cap", steps: Steps{Total: MaxRollupSteps + 1, Done: 0}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.steps.Valid())
		})
	}
}

func TestListedOf(t *testing.T) {
	t.Run("keeps id parent and rollup", func(t *testing.T) {
		listed, invalid, err := ListedOf("env-1", json.RawMessage(
			`[{"id":"ip-1","parent":"plan-1","rollup":{"total":3,"done":2,"fraction":0.66}}]`))
		require.NoError(t, err)
		assert.Equal(t, []Listed{listedAt("env-1", "ip-1", "plan-1", 3, 2)}, listed)
		assert.Empty(t, invalid)
	})
	t.Run("skips ids that are empty, over 128 bytes or hold control characters", func(t *testing.T) {
		raw := fmt.Sprintf(`[{"id":""},{"id":%q},{"id":"a\u0009b"},{"id":"ok"}]`, strings.Repeat("a", 129))
		listed, _, err := ListedOf("env-1", json.RawMessage(raw))
		require.NoError(t, err)
		assert.Equal(t, []Listed{listedAt("env-1", "ok", "", 0, 0)}, listed)
	})
	t.Run("stops at MaxListedPerEnvironment", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("[")
		for i := range MaxListedPerEnvironment + 5 {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"id":"w-%d"}`, i)
		}
		b.WriteString("]")
		listed, _, err := ListedOf("env-1", json.RawMessage(b.String()))
		require.NoError(t, err)
		assert.Len(t, listed, MaxListedPerEnvironment)
	})
	t.Run("refuses an array over 1 MiB", func(t *testing.T) {
		raw := `["` + strings.Repeat("a", maxListedBytes) + `"]`
		_, _, err := ListedOf("env-1", json.RawMessage(raw))
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("decodes an array of exactly 1 MiB", func(t *testing.T) {
		tail := `{"id":"ip-1"}]`
		raw := "[" + strings.Repeat(" ", maxListedBytes-len(tail)-1) + tail
		listed, _, err := ListedOf("env-1", json.RawMessage(raw))
		require.NoError(t, err)
		assert.Equal(t, []Listed{listedAt("env-1", "ip-1", "", 0, 0)}, listed)
	})
	t.Run("returns an out-of-range rollup as zero and names it in invalid", func(t *testing.T) {
		listed, invalid, err := ListedOf("env-1", json.RawMessage(
			`[{"id":"ip-1","rollup":{"total":2,"done":3}},{"id":"ip-2","rollup":{"total":1000000001,"done":0}}]`))
		require.NoError(t, err)
		assert.Equal(t, []Listed{listedAt("env-1", "ip-1", "", 0, 0), listedAt("env-1", "ip-2", "", 0, 0)}, listed)
		assert.Equal(t, []string{"ip-1", "ip-2"}, invalid)
	})
	t.Run("a null rollup is zero", func(t *testing.T) {
		listed, invalid, err := ListedOf("env-1", json.RawMessage(`[{"id":"ip-1","rollup":null}]`))
		require.NoError(t, err)
		assert.Equal(t, []Listed{listedAt("env-1", "ip-1", "", 0, 0)}, listed)
		assert.Empty(t, invalid)
	})
}

func TestMerger_Of(t *testing.T) {
	t.Run("adds a ledger child of the workspace", func(t *testing.T) {
		m := NewMerger(
			[]Listed{listedAt(Local, "p", "", 3, 1), listedAt("env-b", "c", "", 2, 2)},
			[]Entry{ledgerChild("env-b", "c", Local, "p")},
		)
		got, ok := m.Of(Local, "p")
		require.True(t, ok)
		assert.Equal(t, Steps{Total: 5, Done: 3}, got)
	})
	t.Run("adds a ledger child of a same-environment descendant", func(t *testing.T) {
		m := NewMerger(
			[]Listed{listedAt(Local, "p", "", 3, 1), listedAt(Local, "c", "p", 1, 0), listedAt("env-b", "g", "", 2, 2)},
			[]Entry{ledgerChild("env-b", "g", Local, "c")},
		)
		got, ok := m.Of(Local, "p")
		require.True(t, ok)
		assert.Equal(t, Steps{Total: 5, Done: 3}, got)
	})
	t.Run("follows ledger children recursively across three environments", func(t *testing.T) {
		m := NewMerger(
			[]Listed{listedAt(Local, "p", "", 1, 0), listedAt("env-b", "x", "", 1, 0), listedAt("env-c", "z", "", 1, 1)},
			[]Entry{ledgerChild("env-c", "z", "env-b", "x"), ledgerChild("env-b", "x", Local, "p")},
		)
		got, ok := m.Of(Local, "p")
		require.True(t, ok)
		assert.Equal(t, Steps{Total: 3, Done: 1}, got)
	})
	t.Run("counts each ledger entry once", func(t *testing.T) {
		m := NewMerger(
			[]Listed{listedAt(Local, "p", "", 1, 0), listedAt("env-b", "x", "", 2, 1), listedAt("env-b", "x", "", 9, 9)},
			[]Entry{ledgerChild("env-b", "x", Local, "p"), ledgerChild("env-b", "x", Local, "p")},
		)
		got, ok := m.Of(Local, "p")
		require.True(t, ok)
		assert.Equal(t, Steps{Total: 3, Done: 1}, got)
	})
	t.Run("ends a ledger cycle", func(t *testing.T) {
		m := NewMerger(
			[]Listed{listedAt("env-b", "x", "", 1, 0), listedAt("env-c", "z", "", 1, 1)},
			[]Entry{ledgerChild("env-b", "x", "env-c", "z"), ledgerChild("env-c", "z", "env-b", "x")},
		)
		got, ok := m.Of("env-b", "x")
		require.True(t, ok)
		assert.Equal(t, Steps{Total: 2, Done: 1}, got)
	})
	t.Run("stops after MaxMergeDepth ledger hops", func(t *testing.T) {
		listed := []Listed{listedAt(Local, "n-0", "", 1, 1)}
		var ledger []Entry
		parentEnv, parentWS := Local, "n-0"
		for i := 1; i <= MaxMergeDepth+6; i++ {
			env := "env-b"
			if i%2 == 0 {
				env = "env-c"
			}
			ws := fmt.Sprintf("n-%d", i)
			listed = append(listed, listedAt(env, ws, "", 1, 1))
			ledger = append(ledger, ledgerChild(env, ws, parentEnv, parentWS))
			parentEnv, parentWS = env, ws
		}
		m := NewMerger(listed, ledger)
		got, ok := m.Of(Local, "n-0")
		require.True(t, ok)
		assert.Equal(t, Steps{Total: 1 + MaxMergeDepth, Done: 1 + MaxMergeDepth}, got)
	})
	t.Run("ignores a ledger entry on its parent's own environment", func(t *testing.T) {
		m := NewMerger(
			[]Listed{listedAt(Local, "p", "", 1, 0), listedAt(Local, "x", "", 5, 5)},
			[]Entry{ledgerChild(Local, "x", Local, "p")},
		)
		got, ok := m.Of(Local, "p")
		require.True(t, ok)
		assert.Equal(t, Steps{Total: 1, Done: 0}, got)
	})
	t.Run("ends a same-environment parent cycle", func(t *testing.T) {
		m := NewMerger(
			[]Listed{listedAt("env-a", "x", "y", 1, 0), listedAt("env-a", "y", "x", 1, 1), listedAt("env-b", "g", "", 2, 2)},
			[]Entry{ledgerChild("env-b", "g", "env-a", "y")},
		)
		got, ok := m.Of("env-a", "x")
		require.True(t, ok)
		assert.Equal(t, Steps{Total: 3, Done: 2}, got)
	})
	t.Run("reports not listed", func(t *testing.T) {
		m := NewMerger([]Listed{listedAt(Local, "p", "", 1, 0)}, nil)
		got, ok := m.Of(Local, "nope")
		assert.False(t, ok)
		assert.Equal(t, Steps{}, got)
	})
}

func TestMerger_Below(t *testing.T) {
	t.Run("sums only the named workspaces' ledger children", func(t *testing.T) {
		m := NewMerger(
			[]Listed{listedAt(Local, "p", "", 1, 0), listedAt(Local, "q", "", 1, 0), listedAt("env-b", "x", "", 2, 1), listedAt("env-b", "y", "", 4, 4)},
			[]Entry{ledgerChild("env-b", "x", Local, "p"), ledgerChild("env-b", "y", Local, "q")},
		)
		assert.Equal(t, Steps{Total: 2, Done: 1}, m.Below(Local, []string{"p"}))
		assert.Equal(t, Steps{Total: 6, Done: 5}, m.Below(Local, []string{"p", "q"}))
		assert.Equal(t, Steps{}, m.Below(Local, []string{"nope"}))
	})
}
