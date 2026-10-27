package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/review/collect"
)

// submitReview stores a review by profile with one finding, commentID.
func submitReview(t *testing.T, s *Store, profile, commentID string) {
	t.Helper()
	doc := fmt.Appendf(nil, `{"version":"1","verdict":"comment","summary":"s","ref":%q,"profile":%q,"comments":[{"id":%q,"priority":7,"category":"correctness","body":"b","anchors":[],"suggestions":[]}]}`,
		commit, profile, commentID)
	_, err := s.Submit(t.Context(), id(t), commit, profile, doc)
	require.NoError(t, err)
}

// skipped is a decision to skip.
var skipped = Decision{Decision: DecisionSkip, At: time.Date(2026, 10, 26, 18, 30, 0, 0, time.UTC)}

// set returns a change that stores d on finding.
func set(finding string, d Decision) func(*collect.Result, Decisions) error {
	return func(_ *collect.Result, decisions Decisions) error {
		decisions[finding] = d
		return nil
	}
}

func TestStore_Report(t *testing.T) {
	t.Parallel()
	t.Run("nothing stored is an empty report with no decisions", func(t *testing.T) {
		t.Parallel()
		result, decisions, err := New(t.TempDir()).Report(t.Context(), id(t), commit)
		require.NoError(t, err)
		assert.Empty(t, result.Submissions)
		assert.Equal(t, Decisions{}, decisions)
	})
	t.Run("reads the submissions and their decisions", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		submitReview(t, s, "go", "ctx-1")
		require.NoError(t, s.Decide(t.Context(), id(t), commit, set("go:ctx-1", skipped)))
		result, decisions, err := s.Report(t.Context(), id(t), commit)
		require.NoError(t, err)
		require.Len(t, result.Comments, 1)
		assert.Equal(t, Decisions{"go:ctx-1": skipped}, decisions)
	})
	t.Run("rejects a short commit", func(t *testing.T) {
		t.Parallel()
		_, _, err := New(t.TempDir()).Report(t.Context(), id(t), "abc")
		require.ErrorContains(t, err, "isn't a full commit")
	})
	t.Run("an unknown version fails", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		submitReview(t, s, "go", "ctx-1")
		require.NoError(t, os.WriteFile(filepath.Join(s.Dir(id(t), commit), decisionsName), []byte(`{"version":2,"decisions":{}}`), 0o600))
		_, _, err := s.Report(t.Context(), id(t), commit)
		require.ErrorContains(t, err, "version 2 isn't supported")
	})
	t.Run("a corrupt file fails", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		submitReview(t, s, "go", "ctx-1")
		require.NoError(t, os.WriteFile(filepath.Join(s.Dir(id(t), commit), decisionsName), []byte(`{`), 0o600))
		_, _, err := s.Report(t.Context(), id(t), commit)
		require.Error(t, err)
	})
}

func TestStore_Decide(t *testing.T) {
	t.Parallel()
	t.Run("round-trips decisions.json, version 1", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		submitReview(t, s, "security", "dropped-context-1")
		d := Decision{
			Decision: DecisionFix, Suggestion: new(0), Note: "", At: time.Date(2026, 10, 26, 18, 30, 0, 0, time.UTC),
		}
		require.NoError(t, s.Decide(t.Context(), id(t), commit, set("security:dropped-context-1", d)))
		data, err := os.ReadFile(filepath.Join(s.Dir(id(t), commit), decisionsName))
		require.NoError(t, err)
		assert.JSONEq(t, `{"version": 1, "decisions": {
			"security:dropped-context-1": {"decision": "fix", "suggestion": 0, "note": "",
				"at": "2026-10-26T18:30:00Z", "done": null}}}`, string(data))
		d.Done = &Done{FixSent: "fix-1", At: d.At}
		require.NoError(t, s.Decide(t.Context(), id(t), commit, set("security:dropped-context-1", d)))
		_, got, err := s.Report(t.Context(), id(t), commit)
		require.NoError(t, err)
		assert.Equal(t, Decisions{"security:dropped-context-1": d}, got)
		data, err = os.ReadFile(filepath.Join(s.Dir(id(t), commit), decisionsName))
		require.NoError(t, err)
		assert.Contains(t, string(data), `"done": {
        "fix_sent": "fix-1",`)
	})
	t.Run("change sees the current report", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		submitReview(t, s, "go", "ctx-1")
		require.NoError(t, s.Decide(t.Context(), id(t), commit, func(result *collect.Result, _ Decisions) error {
			require.Len(t, result.Comments, 1)
			assert.Equal(t, "go:ctx-1", result.Comments[0].ID)
			return nil
		}))
	})
	t.Run("a failing change stores nothing", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		submitReview(t, s, "go", "ctx-1")
		boom := errors.New("boom")
		err := s.Decide(t.Context(), id(t), commit, func(_ *collect.Result, decisions Decisions) error {
			decisions["go:ctx-1"] = skipped
			return boom
		})
		require.ErrorIs(t, err, boom)
		assert.NoFileExists(t, filepath.Join(s.Dir(id(t), commit), decisionsName))
	})
	t.Run("nothing submitted creates nothing", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		require.NoError(t, s.Decide(t.Context(), id(t), commit, func(result *collect.Result, decisions Decisions) error {
			assert.Empty(t, result.Comments)
			assert.Empty(t, decisions)
			return nil
		}))
		assert.NoDirExists(t, s.Dir(id(t), commit))
	})
	t.Run("rejects a short commit", func(t *testing.T) {
		t.Parallel()
		require.ErrorContains(t, New(t.TempDir()).Decide(t.Context(), id(t), "abc", set("x", Decision{})), "isn't a full commit")
	})
	t.Run("waits for the directory's lock", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		submitReview(t, s, "go", "ctx-1")
		unlock, err := lockfile.Exclusive(t.Context(), filepath.Join(s.Dir(id(t), commit), lockName))
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		err = s.Decide(ctx, id(t), commit, set("go:ctx-1", skipped))
		require.ErrorIs(t, err, context.DeadlineExceeded)
		unlock()
		require.NoError(t, s.Decide(t.Context(), id(t), commit, set("go:ctx-1", skipped)))
	})
	t.Run("concurrent decisions are all kept", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		submitReview(t, s, "go", "ctx-1")
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				assert.NoError(t, s.Decide(t.Context(), id(t), commit, set(fmt.Sprintf("go:f-%d", i), skipped)))
			})
		}
		wg.Wait()
		_, got, err := s.Report(t.Context(), id(t), commit)
		require.NoError(t, err)
		assert.Len(t, got, 8)
	})
}

func TestDropDecisions(t *testing.T) {
	t.Parallel()
	t.Run("a resubmission drops only its profile's decisions", func(t *testing.T) {
		t.Parallel()
		s := New(t.TempDir())
		submitReview(t, s, "go", "ctx-1")
		submitReview(t, s, "go-extra", "x-1")
		for _, f := range []string{"go:ctx-1", "go-extra:x-1"} {
			require.NoError(t, s.Decide(t.Context(), id(t), commit, set(f, skipped)))
		}
		submitReview(t, s, "go", "ctx-1")
		_, got, err := s.Report(t.Context(), id(t), commit)
		require.NoError(t, err)
		assert.Equal(t, Decisions{"go-extra:x-1": skipped}, got)
	})
	t.Run("leaves the file alone with nothing to drop", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, dropDecisions(dir, "go"))
		assert.NoFileExists(t, filepath.Join(dir, decisionsName))
	})
	t.Run("fails on a corrupt file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, decisionsName), []byte(`[`), 0o600))
		assert.Error(t, dropDecisions(dir, "go"))
	})
}
