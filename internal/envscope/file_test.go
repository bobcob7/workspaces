package envscope

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validScopeFile = `{"version":1,"environments":[{"id":"local","name":"local"},{"id":"env-1a2b3c4d","name":"linux-server"},{"id":"env-2b3c4d5e","name":"win-rig"}],"self":"env-1a2b3c4d","source":"granted","parent":null,"written_at":"2026-11-24T10:00:00Z"}`

func writeScopeFile(t *testing.T, stateDir, id, body string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(stateDir, id), 0o700))
	require.NoError(t, os.WriteFile(Path(stateDir, id), []byte(body), mode))
}

func TestPath(t *testing.T) {
	assert.Equal(t, filepath.Join("state", "ws-1", "scope.json"), Path("state", "ws-1"))
}

func TestFileFor(t *testing.T) {
	scope := mustNew(t, serverRef, rigRef)
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.FixedZone("CST", -6*3600))
	parent := &Parent{Environment: Local, Workspace: "plan-9f8e7d6c"}
	f := FileFor(scope, "narrowed", parent, now)
	assert.Equal(t, 1, f.Version)
	assert.Equal(t, scope.Refs(), f.Environments)
	assert.Equal(t, serverRef.ID, f.Self)
	assert.Equal(t, "narrowed", f.Source)
	assert.Equal(t, parent, f.Parent)
	assert.True(t, now.Equal(f.WrittenAt))
	assert.Equal(t, time.UTC, f.WrittenAt.Location())
}

func TestRead(t *testing.T) {
	t.Parallel()
	const id = "ws-1"
	self := serverRef
	t.Run("a missing file is {self} with no error", func(t *testing.T) {
		t.Parallel()
		got, f, err := Read(t.TempDir(), id, self)
		require.NoError(t, err)
		assert.Nil(t, f)
		assert.Equal(t, []string{self.ID}, got.IDs())
	})
	t.Run("a valid file gives its scope", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeScopeFile(t, dir, id, validScopeFile, 0o600)
		got, f, err := Read(dir, id, self)
		require.NoError(t, err)
		require.NotNil(t, f)
		assert.Equal(t, []string{Local, self.ID, rigRef.ID}, got.IDs())
		assert.Equal(t, "granted", f.Source)
	})
	invalid := []struct {
		name string
		body string
	}{
		{"not JSON", `{`},
		{"an unknown field", strings.Replace(validScopeFile, `"source":"granted"`, `"source":"granted","extra":1`, 1)},
		{"version 2", strings.Replace(validScopeFile, `"version":1`, `"version":2`, 1)},
		{"trailing data", validScopeFile + ` {}`},
		{"self missing from its entries", strings.Replace(validScopeFile, `{"id":"env-1a2b3c4d","name":"linux-server"},`, "", 1)},
		{"a bad environment id", strings.Replace(validScopeFile, `"id":"env-2b3c4d5e"`, `"id":"ENV-2B"`, 1)},
		{"an unknown source", strings.Replace(validScopeFile, `"source":"granted"`, `"source":"stolen"`, 1)},
		{"a bad parent environment", strings.Replace(validScopeFile, `"parent":null`, `"parent":{"environment":"x","workspace":"w"}`, 1)},
		{"self of another environment", strings.Replace(validScopeFile, `"self":"env-1a2b3c4d"`, `"self":"env-2b3c4d5e"`, 1)},
		{"more than MaxEntries", tooManyEntriesFile(t)},
		{"larger than MaxFileBytes", strings.Replace(validScopeFile, `"linux-server"`, `"`+strings.Repeat("x", MaxFileBytes)+`"`, 1)},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" is {self} and ErrInvalid", func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeScopeFile(t, dir, id, tt.body, 0o600)
			got, f, err := Read(dir, id, self)
			require.ErrorIs(t, err, ErrInvalid)
			assert.Nil(t, f)
			assert.Equal(t, []string{self.ID}, got.IDs())
		})
	}
	t.Run("a workspace id that isn't one path element is ErrInvalid", func(t *testing.T) {
		t.Parallel()
		got, _, err := Read(t.TempDir(), "../escape", self)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Equal(t, []string{self.ID}, got.IDs())
	})
	t.Run("a group-readable file is {self} and ErrInvalid", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("file modes don't apply on Windows")
		}
		dir := t.TempDir()
		writeScopeFile(t, dir, id, validScopeFile, 0o644)
		got, _, err := Read(dir, id, self)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Equal(t, []string{self.ID}, got.IDs())
	})
}

// tooManyEntriesFile is a scope file with MaxEntries+1 environments, self among them.
func tooManyEntriesFile(t *testing.T) string {
	t.Helper()
	refs := []Ref{serverRef}
	for i := range MaxEntries {
		refs = append(refs, Ref{ID: fmt.Sprintf("env-%08x", i+1), Name: fmt.Sprintf("n%d", i)})
	}
	return marshalFile(t, &File{Version: 1, Environments: refs, Self: serverRef.ID, Source: "granted", WrittenAt: time.Unix(0, 0).UTC()})
}

func marshalFile(t *testing.T, f *File) string {
	t.Helper()
	data, err := json.Marshal(f)
	require.NoError(t, err)
	return string(data)
}

func TestWrite(t *testing.T) {
	t.Parallel()
	const id = "ws-1"
	scope := mustNew(t, serverRef, rigRef)
	t.Run("round-trips through Read", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		f := FileFor(scope, "granted", nil, time.Unix(1_700_000_000, 0))
		require.NoError(t, Write(dir, id, f))
		got, read, err := Read(dir, id, serverRef)
		require.NoError(t, err)
		require.NotNil(t, read)
		assert.Equal(t, scope.IDs(), got.IDs())
		assert.Equal(t, "granted", read.Source)
		assert.Nil(t, read.Parent)
	})
	t.Run("writes mode 0600", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("file modes don't apply on Windows")
		}
		dir := t.TempDir()
		require.NoError(t, Write(dir, id, FileFor(scope, "granted", nil, time.Now())))
		info, err := os.Stat(Path(dir, id))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})
	t.Run("an invalid file is refused and nothing is written", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := Write(dir, id, &File{Version: 2, Self: serverRef.ID})
		require.ErrorIs(t, err, ErrInvalid)
		_, statErr := os.Stat(Path(dir, id))
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	})
	t.Run("a workspace id with a path separator is refused", func(t *testing.T) {
		t.Parallel()
		err := Write(t.TempDir(), "../escape", FileFor(scope, "granted", nil, time.Now()))
		require.ErrorIs(t, err, ErrInvalid)
	})
}
