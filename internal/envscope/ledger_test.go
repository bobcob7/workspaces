package envscope

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envA = "env-1a2b3c4d"
	envB = "env-2b3c4d5e"
)

func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	return NewLedger(t.TempDir())
}

func ledgerEntry(env, workspace string, scope ...string) *Entry {
	return &Entry{
		Environment: env,
		Workspace:   workspace,
		Scope:       scope,
		CreatedAt:   time.Unix(1_700_000_000, 0).UTC(),
	}
}

func TestLedger_Put(t *testing.T) {
	t.Parallel()
	t.Run("adds an entry", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "helper-1", envA, envB)))
		entries, err := l.List()
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, []string{envA, envB}, entries[0].Scope)
	})
	t.Run("replaces the entry with the same environment and workspace", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "helper-1", envA)))
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "helper-1", envA, envB)))
		entries, err := l.List()
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, []string{envA, envB}, entries[0].Scope)
	})
	t.Run("an invalid entry is ErrInvalid", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		bad := map[string]*Entry{
			"environment":        ledgerEntry("local-ish", "helper-1", envA),
			"scope id":           ledgerEntry(envA, "helper-1", "env-XYZ"),
			"parent environment": withParent(ledgerEntry(envA, "helper-1", envA), "nope", "plan-1"),
			"empty workspace":    ledgerEntry(envA, "", envA),
			"nil scope":          ledgerEntry(envA, "helper-1"),
			"empty scope":        ledgerEntry(envA, "helper-1", []string{}...),
			"scope over max":     ledgerEntry(envA, "helper-1", manyScopeIDs(MaxEntries+1)...),
		}
		for name, e := range bad {
			require.ErrorIs(t, l.Put(t.Context(), e), ErrInvalid, name)
		}
	})
	t.Run("a scope of MaxEntries ids is accepted", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "helper-1", manyScopeIDs(MaxEntries)...)))
	})
	t.Run("a full ledger refuses a new key but still replaces one", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		l := NewLedger(dir)
		writeFullLedger(t, dir)
		require.Error(t, l.Put(t.Context(), ledgerEntry(envA, "new-one", envA)))
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "ws-1", envB)))
		entries, err := l.List()
		require.NoError(t, err)
		assert.Len(t, entries, MaxLedgerEntries)
	})
}

func withParent(e *Entry, environment, workspace string) *Entry {
	e.Parent = &Parent{Environment: environment, Workspace: workspace}
	return e
}

// manyScopeIDs returns n distinct valid environment ids.
func manyScopeIDs(n int) []string {
	ids := make([]string, 0, n)
	for i := range n {
		ids = append(ids, fmt.Sprintf("env-%08x", i+1))
	}
	return ids
}

// writeFullLedger writes a ledger with MaxLedgerEntries entries, ws-0 .. ws-4095.
func writeFullLedger(t *testing.T, dir string) {
	t.Helper()
	entries := make([]Entry, 0, MaxLedgerEntries)
	for i := range MaxLedgerEntries {
		entries = append(entries, *ledgerEntry(envA, fmt.Sprintf("ws-%d", i), envA))
	}
	data, err := json.Marshal(ledgerFile{Version: 1, Entries: entries})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ledgerName), data, 0o600))
}

func TestLedger_Remove(t *testing.T) {
	t.Parallel()
	t.Run("removes the entry for the key", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "helper-1", envA)))
		require.NoError(t, l.Remove(t.Context(), envA, "helper-1"))
		_, ok, err := l.Get(envA, "helper-1")
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("a missing key is not an error", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, newTestLedger(t).Remove(t.Context(), envA, "nobody"))
	})
	t.Run("keeps the same workspace on another environment", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "helper-1", envA)))
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envB, "helper-1", envB)))
		require.NoError(t, l.Remove(t.Context(), envA, "helper-1"))
		_, okA, err := l.Get(envA, "helper-1")
		require.NoError(t, err)
		assert.False(t, okA)
		got, okB, err := l.Get(envB, "helper-1")
		require.NoError(t, err)
		require.True(t, okB)
		assert.Equal(t, []string{envB}, got.Scope)
	})
}

func TestLedger_List(t *testing.T) {
	t.Parallel()
	t.Run("a missing file is empty", func(t *testing.T) {
		t.Parallel()
		entries, err := newTestLedger(t).List()
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("returns every entry", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "helper-1", envA)))
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envB, "helper-2", envB)))
		entries, err := l.List()
		require.NoError(t, err)
		assert.Len(t, entries, 2)
	})
	t.Run("a malformed file is ErrInvalid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ledgerName), []byte(`{"version":9}`), 0o600))
		_, err := NewLedger(dir).List()
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("an entry with an empty scope is ErrInvalid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		body := `{"version":1,"entries":[{"environment":"` + envA + `","workspace":"helper-1","scope":[]}]}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, ledgerName), []byte(body), 0o600))
		_, err := NewLedger(dir).List()
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("a file over the size cap is ErrInvalid before it is decoded", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		padding := strings.Repeat(" ", maxLedgerBytes)
		body := `{"version":1,"entries":[]` + padding + `}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, ledgerName), []byte(body), 0o600))
		_, err := NewLedger(dir).List()
		require.ErrorIs(t, err, ErrInvalid)
	})
}

func TestLedger_Get(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "helper-1", envA)))
	t.Run("finds the entry for the key", func(t *testing.T) {
		t.Parallel()
		e, ok, err := l.Get(envA, "helper-1")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, []string{envA}, e.Scope)
	})
	t.Run("the same workspace on another environment is not found", func(t *testing.T) {
		t.Parallel()
		_, ok, err := l.Get(envB, "helper-1")
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

func TestLedger_Ceiling(t *testing.T) {
	t.Parallel()
	t.Run("the nearest ancestor in the chain wins", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "grand", envA, envB)))
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envA, "parent", envA)))
		got, err := l.Ceiling(envA, []string{"child", "parent", "grand"})
		require.NoError(t, err)
		assert.Equal(t, []string{envA}, got)
	})
	t.Run("entries on another environment are ignored", func(t *testing.T) {
		t.Parallel()
		l := newTestLedger(t)
		require.NoError(t, l.Put(t.Context(), ledgerEntry(envB, "parent", envA, envB)))
		got, err := l.Ceiling(envA, []string{"parent"})
		require.NoError(t, err)
		assert.Nil(t, got)
	})
	t.Run("nil when no ancestor has an entry", func(t *testing.T) {
		t.Parallel()
		got, err := newTestLedger(t).Ceiling(envA, []string{"child", "parent"})
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestLedgerExists(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, LedgerExists(dir))
	require.NoError(t, NewLedger(dir).Put(t.Context(), ledgerEntry(envA, "w", envA)))
	assert.True(t, LedgerExists(dir))
}
