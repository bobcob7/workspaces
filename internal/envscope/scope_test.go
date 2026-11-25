package envscope

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	localRef  = Ref{ID: Local, Name: Local}
	serverRef = Ref{ID: "env-1a2b3c4d", Name: "linux-server"}
	rigRef    = Ref{ID: "env-2b3c4d5e", Name: "win-rig"}
)

func mustNew(t *testing.T, self Ref, refs ...Ref) Scope {
	t.Helper()
	s, err := New(self, refs)
	require.NoError(t, err)
	return s
}

func TestNew(t *testing.T) {
	t.Run("adds self and sorts Local first, then by name", func(t *testing.T) {
		s := mustNew(t, serverRef, rigRef, localRef)
		assert.Equal(t, []string{Local, serverRef.ID, rigRef.ID}, s.IDs())
		assert.Equal(t, []string{Local, "linux-server", "win-rig"}, s.Names())
	})
	t.Run("dedupes by id", func(t *testing.T) {
		s := mustNew(t, serverRef, rigRef, rigRef, serverRef)
		assert.Equal(t, []string{serverRef.ID, rigRef.ID}, s.IDs())
	})
	t.Run("an invalid id is ErrInvalid", func(t *testing.T) {
		_, err := New(serverRef, []Ref{{ID: "env-XYZ", Name: "x"}})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("an empty name is ErrInvalid", func(t *testing.T) {
		_, err := New(serverRef, []Ref{{ID: rigRef.ID}})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("a name with a control character is ErrInvalid", func(t *testing.T) {
		for _, name := range []string{"win\nrig", "win\x00rig", "win\trig"} {
			_, err := New(serverRef, []Ref{{ID: rigRef.ID, Name: name}})
			require.ErrorIs(t, err, ErrInvalid, name)
		}
	})
	t.Run("Local must be named local", func(t *testing.T) {
		_, err := New(serverRef, []Ref{{ID: Local, Name: "laptop"}})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("only Local may be named local", func(t *testing.T) {
		_, err := New(serverRef, []Ref{{ID: rigRef.ID, Name: Local}})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("an invalid self is ErrInvalid", func(t *testing.T) {
		_, err := New(Ref{ID: "nope", Name: "x"}, nil)
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("MaxEntries counts self and more is ErrInvalid", func(t *testing.T) {
		full := make([]Ref, 0, MaxEntries)
		for i := range MaxEntries - 1 {
			full = append(full, Ref{ID: fmt.Sprintf("env-%08x", i+1), Name: fmt.Sprintf("n%d", i)})
		}
		s, err := New(serverRef, full)
		require.NoError(t, err)
		assert.Len(t, s.Refs(), MaxEntries)
		_, err = New(serverRef, append(full, rigRef))
		require.ErrorIs(t, err, ErrInvalid)
	})
}

func TestScope_Narrow(t *testing.T) {
	scope := mustNew(t, serverRef, rigRef)
	t.Run("a subset narrows to self plus those ids", func(t *testing.T) {
		got, err := scope.Narrow(serverRef, []string{rigRef.ID})
		require.NoError(t, err)
		assert.Equal(t, []string{serverRef.ID, rigRef.ID}, got.IDs())
		assert.Equal(t, serverRef, got.Self())
	})
	t.Run("nil ids narrows to self alone", func(t *testing.T) {
		got, err := scope.Narrow(serverRef, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{serverRef.ID}, got.IDs())
	})
	t.Run("a superset id is an OutOfScopeError naming it", func(t *testing.T) {
		_, err := scope.Narrow(serverRef, []string{rigRef.ID, localRef.ID})
		require.ErrorIs(t, err, ErrOutOfScope)
		var outOfScope *OutOfScopeError
		require.ErrorAs(t, err, &outOfScope)
		assert.Equal(t, Local, outOfScope.Environment)
		assert.Equal(t, []string{"linux-server", "win-rig"}, outOfScope.Allowed)
	})
	t.Run("self outside the scope is an OutOfScopeError", func(t *testing.T) {
		_, err := Only(serverRef).Narrow(rigRef, nil)
		require.ErrorIs(t, err, ErrOutOfScope)
	})
}

func TestScope_Intersect(t *testing.T) {
	scope := mustNew(t, serverRef, rigRef)
	t.Run("keeps the listed refs that are in the scope", func(t *testing.T) {
		got := scope.Intersect([]string{rigRef.ID, "env-9f9f9f9f"})
		assert.Equal(t, []string{serverRef.ID, rigRef.ID}, got.IDs())
	})
	t.Run("keeps self even when ids is empty", func(t *testing.T) {
		got := scope.Intersect(nil)
		assert.Equal(t, []string{serverRef.ID}, got.IDs())
	})
}

func TestScope_Contains(t *testing.T) {
	scope := mustNew(t, serverRef, rigRef)
	assert.True(t, scope.Contains(serverRef.ID))
	assert.True(t, scope.Contains(rigRef.ID))
	assert.False(t, scope.Contains(localRef.ID))
}

func TestScope_ByName(t *testing.T) {
	scope := mustNew(t, serverRef, rigRef)
	ref, ok := scope.ByName("win-rig")
	assert.True(t, ok)
	assert.Equal(t, rigRef, ref)
	_, ok = scope.ByName("nope")
	assert.False(t, ok)
}

func TestScope_WiderThanSelf(t *testing.T) {
	assert.False(t, Only(serverRef).WiderThanSelf())
	assert.True(t, mustNew(t, serverRef, rigRef).WiderThanSelf())
}

func TestScope_Refs(t *testing.T) {
	scope := mustNew(t, serverRef, rigRef)
	refs := scope.Refs()
	refs[0] = localRef
	assert.Equal(t, serverRef.ID, scope.Refs()[0].ID)
}

func TestOutOfScopeError_Error(t *testing.T) {
	err := &OutOfScopeError{Environment: "local", Allowed: []string{"linux-server", "win-rig"}}
	assert.Equal(t, "environment local isn't in your scope (linux-server, win-rig)", err.Error())
	assert.ErrorIs(t, err, ErrOutOfScope)
}
