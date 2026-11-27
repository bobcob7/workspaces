package artifact

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newScanEnv is a store with one artifact and a state dir holding workspace
// w1's record.
func newScanEnv(t *testing.T) (*Store, string, string, Record) {
	t.Helper()
	s, dir := newTestStore(t, fixedClock(testBase))
	rec := addText(t, s, "a.md", "# A\n")
	stateDir := filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.MkdirAll(filepath.Join(stateDir, "w1"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "w1", workspaceRecord), nil, 0o600))
	return s, dir, stateDir, rec
}

func citedCount(t *testing.T, dir, id string) int {
	t.Helper()
	links, err := readLinks(filepath.Join(dir, id))
	require.NoError(t, err)
	n := 0
	for _, l := range links {
		if l.Action == ActionCited {
			n += l.Count
		}
	}
	return n
}

func TestStore_RecordScan(t *testing.T) {
	t.Run("the same scan twice counts each message once", func(t *testing.T) {
		t.Parallel()
		s, dir, stateDir, rec := newScanEnv(t)
		scan := &Scan{Workspace: "w1", Transcript: "/t.jsonl", Offset: 100, Citations: []Citation{
			{Key: "m1", At: testBase.Add(-time.Hour), Refs: []Ref{{ID: rec.ID}, {ID: rec.ID}}},
			{Key: "m2", Refs: []Ref{{ID: rec.ID, Rev: 1}}},
		}}
		res, err := s.RecordScan(t.Context(), stateDir, scan)
		require.NoError(t, err)
		assert.Equal(t, 2, res.Recorded)
		res, err = s.RecordScan(t.Context(), stateDir, scan)
		require.NoError(t, err)
		assert.Equal(t, 0, res.Recorded)
		assert.Equal(t, 2, res.Skipped)
		assert.Equal(t, 2, citedCount(t, dir, rec.ID))
	})
	t.Run("a citation keeps its own time", func(t *testing.T) {
		t.Parallel()
		s, dir, stateDir, rec := newScanEnv(t)
		_, err := s.RecordScan(t.Context(), stateDir, &Scan{Workspace: "w1", Citations: []Citation{{Key: "m1", At: testBase.Add(-time.Hour), Refs: []Ref{{ID: rec.ID}}}}})
		require.NoError(t, err)
		links, err := readLinks(filepath.Join(dir, rec.ID))
		require.NoError(t, err)
		assert.Equal(t, testBase.Add(-time.Hour), links[0].FirstAt)
	})
	t.Run("an unknown artifact is skipped, not seen", func(t *testing.T) {
		t.Parallel()
		s, _, stateDir, _ := newScanEnv(t)
		res, err := s.RecordScan(t.Context(), stateDir, &Scan{Workspace: "w1", Citations: []Citation{{Key: "m1", Refs: []Ref{{ID: "art-00000000"}}}}})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Skipped)
		state, err := readScanState(filepath.Join(stateDir, "w1"))
		require.NoError(t, err)
		assert.Empty(t, state.Seen)
	})
	t.Run("a failed link write isn't seen, so a retry records it", func(t *testing.T) {
		t.Parallel()
		s, dir, stateDir, rec := newScanEnv(t)
		path := filepath.Join(dir, rec.ID, linksFile)
		require.NoError(t, os.WriteFile(path, []byte(`{"version":9}`), 0o600))
		scan := &Scan{Workspace: "w1", Transcript: "/t", Offset: 100, Citations: []Citation{{Key: "m1", Refs: []Ref{{ID: rec.ID}}}}}
		res, err := s.RecordScan(t.Context(), stateDir, scan)
		require.NoError(t, err)
		assert.Len(t, res.Warnings, 1)
		assert.Equal(t, 0, res.Recorded)
		assert.Equal(t, 1, res.Failed)
		assert.Equal(t, int64(0), res.Offset, "the cursor stays, so the desktop sends the citation again")
		require.NoError(t, os.Remove(path))
		res, err = s.RecordScan(t.Context(), stateDir, scan)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Recorded)
		assert.Equal(t, int64(100), res.Offset)
	})
	t.Run("a done context fails the rest without trying them", func(t *testing.T) {
		t.Parallel()
		s, dir, _, rec := newScanEnv(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		state := scanState{}
		res := s.recordPairs(ctx, &Scan{Workspace: "w1", Citations: []Citation{{Key: "m1", Refs: []Ref{{ID: rec.ID}}}}}, &state)
		assert.Equal(t, 1, res.Failed)
		assert.Empty(t, state.Seen)
		_, statErr := os.Stat(filepath.Join(dir, rec.ID, linksFile))
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	})
	t.Run("refuses a workspace with no record, and makes nothing", func(t *testing.T) {
		t.Parallel()
		s, _, stateDir, _ := newScanEnv(t)
		_, err := s.RecordScan(t.Context(), stateDir, &Scan{Workspace: "gone-1"})
		require.ErrorIs(t, err, ErrNoWorkspace)
		_, statErr := os.Stat(filepath.Join(stateDir, "gone-1"))
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	})
	t.Run("refuses a newer citations.json", func(t *testing.T) {
		t.Parallel()
		s, _, stateDir, _ := newScanEnv(t)
		require.NoError(t, os.WriteFile(filepath.Join(stateDir, "w1", citationsFile), []byte(`{"version":2}`), 0o600))
		_, err := s.RecordScan(t.Context(), stateDir, &Scan{Workspace: "w1"})
		require.ErrorIs(t, err, ErrNewer)
	})
}

func TestStore_RecordScan_cursor(t *testing.T) {
	tests := []struct {
		name       string
		first      Scan
		second     Scan
		transcript string
		offset     int64
	}{
		{name: "keeps the larger offset of one transcript", first: Scan{Transcript: "/a", Offset: 500}, second: Scan{Transcript: "/a", Offset: 200}, transcript: "/a", offset: 500},
		{name: "moves forward on one transcript", first: Scan{Transcript: "/a", Offset: 200}, second: Scan{Transcript: "/a", Offset: 500}, transcript: "/a", offset: 500},
		{name: "a new transcript replaces both", first: Scan{Transcript: "/a", Offset: 500}, second: Scan{Transcript: "/b", Offset: 10}, transcript: "/b", offset: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, _, stateDir, _ := newScanEnv(t)
			tt.first.Workspace, tt.second.Workspace = "w1", "w1"
			_, err := s.RecordScan(t.Context(), stateDir, &tt.first)
			require.NoError(t, err)
			res, err := s.RecordScan(t.Context(), stateDir, &tt.second)
			require.NoError(t, err)
			assert.Equal(t, tt.offset, res.Offset)
			cursors, _, err := Cursors(stateDir)
			require.NoError(t, err)
			require.Len(t, cursors, 1)
			assert.Equal(t, tt.transcript, cursors[0].Transcript)
			assert.Equal(t, testBase, cursors[0].UpdatedAt)
		})
	}
}

func TestAdvanceCursor_seenCap(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{name: "4095 pairs are kept", n: MaxSeen - 1, want: MaxSeen - 1},
		{name: "4096 pairs are kept", n: MaxSeen, want: MaxSeen},
		{name: "4097 pairs drop the oldest", n: MaxSeen + 1, want: MaxSeen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := scanState{}
			for i := range tt.n {
				state.Seen = append(state.Seen, fmt.Sprintf("m%d:art-1", i))
			}
			advanceCursor(&state, &Scan{}, true, testBase)
			assert.Len(t, state.Seen, tt.want)
			assert.Equal(t, fmt.Sprintf("m%d:art-1", tt.n-1), state.Seen[len(state.Seen)-1])
		})
	}
}

func TestStore_RecordScan_evictedPairCountsAgain(t *testing.T) {
	t.Parallel()
	s, dir, stateDir, rec := newScanEnv(t)
	seen := make([]string, MaxSeen)
	for i := range seen {
		seen[i] = fmt.Sprintf("old%d:%s", i, rec.ID)
	}
	seen[0] = "m1:" + rec.ID
	require.NoError(t, writeScanState(filepath.Join(stateDir, "w1"), &scanState{Version: 1, Seen: seen}))
	scan := &Scan{Workspace: "w1", Citations: []Citation{{Key: "m2", Refs: []Ref{{ID: rec.ID}}}}}
	_, err := s.RecordScan(t.Context(), stateDir, scan)
	require.NoError(t, err)
	scan.Citations[0].Key = "m1"
	res, err := s.RecordScan(t.Context(), stateDir, scan)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Recorded, "m1 was evicted by m2, so it counts again (accepted, ADR 0084)")
	assert.Equal(t, 2, citedCount(t, dir, rec.ID))
}

func TestCheckScan(t *testing.T) {
	citations := func(n int) []Citation {
		c := make([]Citation, n)
		for i := range c {
			c[i] = Citation{Key: fmt.Sprintf("m%d", i)}
		}
		return c
	}
	tests := []struct {
		name string
		scan Scan
		ok   bool
	}{
		{name: "a plain scan", scan: Scan{Workspace: "w1"}, ok: true},
		{name: "1000 citations", scan: Scan{Workspace: "w1", Citations: citations(MaxScanCitations)}, ok: true},
		{name: "1001 citations", scan: Scan{Workspace: "w1", Citations: citations(MaxScanCitations + 1)}},
		{name: "a 128-byte key", scan: Scan{Workspace: "w1", Citations: []Citation{{Key: strings.Repeat("k", MaxScanKeyBytes)}}}, ok: true},
		{name: "a 129-byte key", scan: Scan{Workspace: "w1", Citations: []Citation{{Key: strings.Repeat("k", MaxScanKeyBytes+1)}}}},
		{name: "an empty key", scan: Scan{Workspace: "w1", Citations: []Citation{{}}}},
		{name: "a negative offset", scan: Scan{Workspace: "w1", Offset: -1}},
		{name: "a parent path", scan: Scan{Workspace: "../x"}},
		{name: "an absolute path", scan: Scan{Workspace: "/etc"}},
		{name: "a hidden name", scan: Scan{Workspace: ".loops"}},
		{name: "an empty id", scan: Scan{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := CheckScan(&tt.scan)
			if tt.ok {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestCursors(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(stateDir, "w1"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(stateDir, "w2"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(stateDir, ".loops"), 0o700))
	require.NoError(t, writeScanState(filepath.Join(stateDir, "w1"), &scanState{Version: 1, Transcript: "/t", Offset: 7}))
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "w2", citationsFile), []byte("{"), 0o600))
	cursors, warnings, err := Cursors(stateDir)
	require.NoError(t, err)
	require.Len(t, cursors, 1)
	assert.Equal(t, Cursor{Workspace: "w1", Transcript: "/t", Offset: 7}, cursors[0])
	assert.Len(t, warnings, 1)
}
