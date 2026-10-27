package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/repo"
)

const commit = "3f2a9c1e3b7d5f0c8a1e2d4b6c8f0a2e4d6b8c0f"

func id(t *testing.T) repo.ID {
	t.Helper()
	i, err := repo.NewID("github.com", "bobcob7", "buff-bear")
	require.NoError(t, err)
	return i
}

func TestStore_Dir(t *testing.T) {
	nested, err := repo.NewID("gitlab.example.com:2222", "group/sub", "app")
	require.NoError(t, err)
	s := New(filepath.Join("cfg", "reviews"))
	assert.Equal(t, filepath.Join("cfg", "reviews", "github.com", "bobcob7", "buff-bear", commit), s.Dir(id(t), commit))
	assert.Equal(t, filepath.Join("cfg", "reviews", "gitlab.example.com:2222", "group", "sub", "app", commit), s.Dir(nested, commit))
}

func TestStore_Submit(t *testing.T) {
	t.Parallel()
	t.Run("stores the first submission as <profile>.json", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		got, err := s.Submit(t.Context(), id(t), commit, "security", []byte(`{"a":1}`))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(s.Dir(id(t), commit), "security.json"), got.Path)
		assert.Empty(t, got.Superseded)
		data, err := os.ReadFile(got.Path)
		require.NoError(t, err)
		assert.JSONEq(t, `{"a":1}`, string(data))
	})
	t.Run("a resubmission renames the previous one to the next ordinal", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		dir := s.Dir(id(t), commit)
		for i := range 3 {
			got, err := s.Submit(t.Context(), id(t), commit, "go", fmt.Appendf(nil, `{"n":%d}`, i))
			require.NoError(t, err)
			if i > 0 {
				assert.Equal(t, filepath.Join(dir, fmt.Sprintf("go.%d.json", i)), got.Superseded)
			}
		}
		for path, want := range map[string]string{"go.1.json": `{"n":0}`, "go.2.json": `{"n":1}`, "go.json": `{"n":2}`} {
			data, err := os.ReadFile(filepath.Join(dir, path))
			require.NoError(t, err)
			assert.JSONEq(t, want, string(data), path)
		}
	})
	t.Run("concurrent submissions each get their own ordinal", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				_, err := s.Submit(t.Context(), id(t), commit, "tests", fmt.Appendf(nil, `{"n":%d}`, i))
				assert.NoError(t, err)
			})
		}
		wg.Wait()
		files, err := s.Files(t.Context(), id(t), commit)
		require.NoError(t, err)
		require.Len(t, files, 8)
		for i, f := range files[:7] {
			assert.Equal(t, i+1, f.Ordinal)
		}
		assert.True(t, files[7].Current())
	})
	t.Run("rejects a short commit and a bad profile name", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		_, err := s.Submit(t.Context(), id(t), "3f2a9c1", "go", nil)
		require.ErrorContains(t, err, "isn't a full commit")
		_, err = s.Submit(t.Context(), id(t), commit, "../go", nil)
		require.ErrorContains(t, err, "isn't a profile name")
	})
	t.Run("fails when the store can't be created", func(t *testing.T) {
		t.Parallel()
		root := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(root, nil, 0o600))
		_, err := New(root).Submit(t.Context(), id(t), commit, "go", nil)
		require.Error(t, err)
	})
}

func TestStore_Files(t *testing.T) {
	t.Parallel()
	t.Run("a commit without submissions is empty", func(t *testing.T) {
		t.Parallel()
		files, err := New(t.TempDir()).Files(t.Context(), id(t), commit)
		require.NoError(t, err)
		assert.Empty(t, files)
		assert.NotNil(t, files)
	})
	t.Run("lists by profile, oldest first, and skips other names", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		dir := s.Dir(id(t), commit)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub.json"), 0o750))
		for _, name := range []string{"tests.json", "go.10.json", "go.json", "go.2.json", "decisions.txt", ".tmp.json", "Go.json", "go.0.json"} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600))
		}
		files, err := s.Files(t.Context(), id(t), commit)
		require.NoError(t, err)
		got := []string{}
		for _, f := range files {
			got = append(got, filepath.Base(f.Path))
		}
		assert.Equal(t, []string{"go.2.json", "go.10.json", "go.json", "tests.json"}, got)
	})
	t.Run("rejects a short commit", func(t *testing.T) {
		t.Parallel()
		_, err := New(t.TempDir()).Files(t.Context(), id(t), "abc")
		require.Error(t, err)
	})
}

func TestStore_Collect(t *testing.T) {
	t.Parallel()
	review := func(profile, verdict, commentID string) []byte {
		return fmt.Appendf(nil, `{"version":"1","verdict":%q,"summary":"s","ref":%q,"profile":%q,"comments":[{"id":%q,"priority":7,"category":"correctness","body":"b","anchors":[],"suggestions":[]}]}`,
			verdict, commit, profile, commentID)
	}
	t.Run("current and superseded submissions, with qualified ids", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		for _, sub := range []struct{ profile, verdict, comment string }{
			{"security", "comment", "old-1"},
			{"security", "request_changes", "new-1"},
			{"go", "comment", "ctx-1"},
		} {
			_, err := s.Submit(t.Context(), id(t), commit, sub.profile, review(sub.profile, sub.verdict, sub.comment))
			require.NoError(t, err)
		}
		result, err := s.Collect(t.Context(), id(t), commit)
		require.NoError(t, err)
		assert.Zero(t, result.Unreadable)
		require.Len(t, result.Submissions, 3)
		assert.Equal(t, "go", result.Submissions[0].Profile)
		assert.Nil(t, result.Submissions[0].SupersededBy)
		assert.Equal(t, "security", result.Submissions[1].Profile)
		assert.Equal(t, "comment", result.Submissions[1].Verdict)
		require.NotNil(t, result.Submissions[1].SupersededBy)
		assert.Equal(t, 3, *result.Submissions[1].SupersededBy)
		assert.Equal(t, "request_changes", result.Submissions[2].Verdict)
		ids := []string{}
		for _, c := range result.Comments {
			ids = append(ids, c.ID)
		}
		assert.ElementsMatch(t, []string{"go:ctx-1", "#2:old-1", "security:new-1"}, ids)
	})
	t.Run("an unreadable file is counted, not fatal", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		_, err := s.Submit(t.Context(), id(t), commit, "go", []byte("not json"))
		require.NoError(t, err)
		result, err := s.Collect(t.Context(), id(t), commit)
		require.NoError(t, err)
		assert.Equal(t, 1, result.Unreadable)
		assert.Empty(t, result.Submissions)
	})
	t.Run("nothing stored is an empty report", func(t *testing.T) {
		t.Parallel()
		result, err := New(t.TempDir()).Collect(t.Context(), id(t), commit)
		require.NoError(t, err)
		assert.Empty(t, result.Submissions)
	})
}
