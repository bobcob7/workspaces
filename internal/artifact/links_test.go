package artifact

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

func TestStore_RecordLinks(t *testing.T) {
	t.Run("a repeat updates count and last_at, not a new entry", func(t *testing.T) {
		t.Parallel()
		now := testBase
		s, dir := newTestStore(t, func() time.Time { return now })
		rec := addText(t, s, "a.md", "# A\n")
		read := NewLink{Workspace: "fix-1", Name: "fix", Root: "plan-1", Action: ActionRead, Rev: 1}
		require.NoError(t, s.RecordLinks(t.Context(), rec.ID, read))
		now = testBase.Add(time.Minute)
		require.NoError(t, s.RecordLinks(t.Context(), rec.ID, read))
		links, err := readLinks(filepath.Join(dir, rec.ID))
		require.NoError(t, err)
		require.Len(t, links, 1)
		assert.Equal(t, 2, links[0].Count)
		assert.Equal(t, testBase, links[0].FirstAt)
		assert.Equal(t, testBase.Add(time.Minute), links[0].LastAt)
		assert.Equal(t, "plan-1", *links[0].Root)
	})
	t.Run("workspace, action and revision each make their own entry", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "a.md", "# A\n")
		require.NoError(t, s.RecordLinks(t.Context(), rec.ID,
			NewLink{Workspace: "w1", Action: ActionRead, Rev: 1},
			NewLink{Workspace: "w1", Action: ActionCited},
			NewLink{Workspace: "w1", Action: ActionCited, Rev: 1},
			NewLink{Workspace: "w2", Action: ActionCited},
		))
		links, err := readLinks(filepath.Join(dir, rec.ID))
		require.NoError(t, err)
		assert.Len(t, links, 4)
	})
	t.Run("a time after now is clamped to now", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "a.md", "# A\n")
		require.NoError(t, s.RecordLinks(t.Context(), rec.ID, NewLink{Workspace: "w1", Action: ActionCited, At: testBase.Add(time.Hour)}))
		links, err := readLinks(filepath.Join(dir, rec.ID))
		require.NoError(t, err)
		assert.Equal(t, testBase, links[0].LastAt)
	})
	t.Run("refuses other actions and an empty workspace", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "a.md", "# A\n")
		require.ErrorIs(t, s.RecordLinks(t.Context(), rec.ID, NewLink{Workspace: "w1", Action: ActionCreated}), ErrInvalid)
		require.ErrorIs(t, s.RecordLinks(t.Context(), rec.ID, NewLink{Action: ActionRead}), ErrInvalid)
	})
	t.Run("an unknown artifact is ErrNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		require.ErrorIs(t, s.RecordLinks(t.Context(), "art-00000000", NewLink{Workspace: "w1", Action: ActionRead}), ErrNotFound)
	})
	t.Run("gives up when the lock is held past the bound", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		s.linkLockWait = 20 * time.Millisecond
		rec := addText(t, s, "a.md", "# A\n")
		unlock, err := lockfile.Exclusive(t.Context(), filepath.Join(dir, rec.ID, lockFileName))
		require.NoError(t, err)
		defer unlock()
		err = s.RecordLinks(t.Context(), rec.ID, NewLink{Workspace: "w1", Action: ActionRead})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		_, statErr := os.Stat(filepath.Join(dir, rec.ID, linksFile))
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	})
	t.Run("leaves a newer links.json alone", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "a.md", "# A\n")
		path := filepath.Join(dir, rec.ID, linksFile)
		require.NoError(t, os.WriteFile(path, []byte(`{"version":2,"links":[]}`), 0o600))
		require.ErrorIs(t, s.RecordLinks(t.Context(), rec.ID, NewLink{Workspace: "w1", Action: ActionRead}), ErrNewer)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.JSONEq(t, `{"version":2,"links":[]}`, string(data))
	})
	t.Run("moves a corrupt links.json aside and starts a new one", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "a.md", "# A\n")
		require.NoError(t, os.WriteFile(filepath.Join(dir, rec.ID, linksFile), []byte("{"), 0o600))
		require.NoError(t, s.RecordLinks(t.Context(), rec.ID, NewLink{Workspace: "w1", Action: ActionRead}))
		links, err := readLinks(filepath.Join(dir, rec.ID))
		require.NoError(t, err)
		assert.Len(t, links, 1)
		kept, err := os.ReadFile(filepath.Join(dir, rec.ID, corruptLinksFile))
		require.NoError(t, err)
		assert.Equal(t, "{", string(kept))
	})
	t.Run("returns a links.json it can't read, and writes nothing", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "a.md", "# A\n")
		require.NoError(t, os.Mkdir(filepath.Join(dir, rec.ID, linksFile), 0o700))
		require.Error(t, s.RecordLinks(t.Context(), rec.ID, NewLink{Workspace: "w1", Action: ActionRead}))
		info, err := os.Stat(filepath.Join(dir, rec.ID, linksFile))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})
}

func TestStore_RecordLinks_concurrent(t *testing.T) {
	t.Parallel()
	s, dir := newTestStore(t, fixedClock(testBase))
	rec := addText(t, s, "a.md", "# A\n")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			assert.NoError(t, s.RecordLinks(t.Context(), rec.ID, NewLink{Workspace: "w1", Action: ActionRead, Rev: 1}))
		})
	}
	wg.Wait()
	links, err := readLinks(filepath.Join(dir, rec.ID))
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, 8, links[0].Count)
}

func TestCapLinks(t *testing.T) {
	t.Parallel()
	linksOf := func(n int) []Link {
		links := make([]Link, n)
		for i := range links {
			links[i] = Link{Workspace: fmt.Sprintf("w%d", i), Action: ActionRead, LastAt: testBase.Add(time.Duration(i) * time.Second), Count: 1}
		}
		return links
	}
	tests := []struct {
		name     string
		n        int
		want     int
		evictsW0 bool
	}{
		{name: "499 entries are kept", n: MaxLinks - 1, want: MaxLinks - 1},
		{name: "500 entries are kept", n: MaxLinks, want: MaxLinks},
		{name: "501 entries drop the oldest", n: MaxLinks + 1, want: MaxLinks, evictsW0: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := capLinks(linksOf(tt.n))
			assert.Len(t, got, tt.want)
			kept := false
			for _, l := range got {
				kept = kept || l.Workspace == "w0"
			}
			assert.Equal(t, !tt.evictsW0, kept)
		})
	}
	t.Run("a tie on last_at keeps the earlier entry", func(t *testing.T) {
		t.Parallel()
		links := linksOf(MaxLinks + 1)
		for i := range links {
			links[i].LastAt = testBase
		}
		got := capLinks(links)
		assert.Equal(t, "w0", got[0].Workspace)
		assert.Equal(t, fmt.Sprintf("w%d", MaxLinks-1), got[MaxLinks-1].Workspace)
	})
}

func TestConversations(t *testing.T) {
	t.Parallel()
	at := func(m int) time.Time { return testBase.Add(time.Duration(m) * time.Minute) }
	rec := &Record{Revisions: []Revision{
		{Rev: 1, CreatedAt: at(0), CreatedBy: Agent("pitch-1", "pitch")},
		{Rev: 2, CreatedAt: at(5), CreatedBy: Person()},
		{Rev: 3, CreatedAt: at(10), CreatedBy: Agent("pitch-1", "pitch")},
	}}
	resolvedAt := at(30)
	resolver := Agent("fix-1", "fix")
	threads := []Thread{{
		Comments:   []Comment{{Author: Agent("pitch-1", "pitch"), CreatedAt: at(12)}, {Author: Person(), CreatedAt: at(13)}},
		Resolved:   true,
		ResolvedAt: &resolvedAt,
		ResolvedBy: &resolver,
	}}
	rev3 := 3
	links := []Link{
		{Workspace: "fix-1", Name: new("fix"), Action: ActionRead, Rev: &rev3, FirstAt: at(20), LastAt: at(25), Count: 2},
		{Workspace: "fix-1", Action: ActionCited, FirstAt: at(15), LastAt: at(15), Count: 1},
		{Workspace: "bench-1", Name: new("bench"), Environment: new("win-rig"), Action: ActionCited, FirstAt: at(40), LastAt: at(40), Count: 1},
	}
	got := Conversations(rec, threads, links)
	require.Len(t, got, 3)
	assert.Equal(t, "bench-1", got[0].Workspace)
	assert.Equal(t, "win-rig", *got[0].Environment)
	assert.Equal(t, "fix-1", got[1].Workspace)
	assert.Equal(t, []string{ActionResolved, ActionRead, ActionCited}, got[1].Actions)
	assert.Equal(t, []int{3}, got[1].Revs)
	assert.Equal(t, at(15), got[1].FirstAt)
	assert.Equal(t, at(30), got[1].LastAt)
	assert.Equal(t, "pitch-1", got[2].Workspace)
	assert.Equal(t, []string{ActionCreated, ActionRevised, ActionCommented}, got[2].Actions)
	assert.Equal(t, []int{1, 3}, got[2].Revs)
	assert.Equal(t, "pitch", *got[2].Name)
	assert.Nil(t, got[2].Environment)
}

func TestConversations_tiesAndNames(t *testing.T) {
	t.Parallel()
	rec := &Record{Revisions: []Revision{{Rev: 1, CreatedAt: testBase, CreatedBy: Agent("b-1", "old-name")}}}
	links := []Link{
		{Workspace: "a-1", Action: ActionRead, FirstAt: testBase, LastAt: testBase, Count: 1},
		{Workspace: "b-1", Name: new("new-name"), Action: ActionRead, FirstAt: testBase.Add(time.Minute), LastAt: testBase.Add(time.Minute), Count: 1},
		{Workspace: "c-1", Action: ActionCited, FirstAt: testBase.Add(time.Minute), LastAt: testBase.Add(time.Minute), Count: 1},
	}
	got := Conversations(rec, nil, links)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"b-1", "c-1", "a-1"}, []string{got[0].Workspace, got[1].Workspace, got[2].Workspace}, "same last_at: by workspace")
	assert.Equal(t, "new-name", *got[0].Name, "the latest name wins")
	assert.Nil(t, got[1].Name)
}

func TestConversations_personOnly(t *testing.T) {
	t.Parallel()
	rec := &Record{Revisions: []Revision{{Rev: 1, CreatedAt: testBase, CreatedBy: Person()}}}
	assert.Empty(t, Conversations(rec, nil, nil))
}

func TestStore_Show_conversations(t *testing.T) {
	t.Run("lists the creator and stored links", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, fixedClock(testBase))
		rec, err := s.Add(t.Context(), &NewArtifact{Name: "a.md", Data: []byte("# A\n"), By: Agent("pitch-1", "pitch")})
		require.NoError(t, err)
		require.NoError(t, s.RecordLinks(t.Context(), rec.ID, NewLink{Workspace: "fix-1", Action: ActionRead, Rev: 1}))
		e, err := s.Show(rec.ID)
		require.NoError(t, err)
		require.Len(t, e.Conversations, 2)
		assert.Empty(t, e.Warnings)
		byWorkspace := map[string][]string{}
		for _, c := range e.Conversations {
			byWorkspace[c.Workspace] = c.Actions
		}
		assert.Equal(t, map[string][]string{"pitch-1": {ActionCreated}, "fix-1": {ActionRead}}, byWorkspace)
	})
	t.Run("ignores a newer links.json with a warning", func(t *testing.T) {
		t.Parallel()
		s, dir := newTestStore(t, fixedClock(testBase))
		rec := addText(t, s, "a.md", "# A\n")
		require.NoError(t, os.WriteFile(filepath.Join(dir, rec.ID, linksFile), []byte(`{"version":9,"links":[]}`), 0o600))
		e, err := s.Show(rec.ID)
		require.NoError(t, err)
		require.Len(t, e.Warnings, 1)
		assert.Contains(t, e.Warnings[0], "links ignored")
		_, warnings, err := s.List()
		require.NoError(t, err)
		assert.Len(t, warnings, 1)
	})
}

func TestConversations_remoteAgent(t *testing.T) {
	t.Parallel()
	remote := RemoteAgent("plan", "planner", "linux-server")
	rec := &Record{Revisions: []Revision{{Rev: 1, CreatedAt: testBase, CreatedBy: remote}}}
	got := Conversations(rec, nil, nil)
	require.Len(t, got, 1)
	assert.Equal(t, "plan", got[0].Workspace)
	require.NotNil(t, got[0].Environment)
	assert.Equal(t, "linux-server", *got[0].Environment)
	assert.Equal(t, []string{ActionCreated}, got[0].Actions)
}
