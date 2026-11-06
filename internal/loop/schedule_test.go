package loop

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func at(hour, minute, second int) time.Time {
	return time.Date(2026, 11, 5, hour, minute, second, 0, time.UTC)
}

func TestDue(t *testing.T) {
	t.Parallel()
	next := at(10, 0, 0)
	tests := []struct {
		name   string
		paused bool
		now    time.Time
		want   bool
	}{
		{name: "one second before", now: next.Add(-time.Second), want: false},
		{name: "exactly at", now: next, want: true},
		{name: "after", now: next.Add(time.Minute), want: true},
		{name: "paused and due", paused: true, now: next, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := Loop{NextRunAt: next, Paused: tt.paused}
			assert.Equal(t, tt.want, l.Due(tt.now))
		})
	}
}

func TestAdvance(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{name: "next interval is still after now", now: at(10, 0, 30), want: at(11, 0, 0)},
		{name: "next interval is past now", now: at(13, 20, 0), want: at(14, 20, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := Loop{Name: "a", Every: "1h", NextRunAt: at(10, 0, 0)}
			require.NoError(t, l.Advance(tt.now))
			assert.True(t, tt.want.Equal(l.NextRunAt), "got %s", l.NextRunAt)
		})
	}
}

func TestAdvanceRejectsBadPeriod(t *testing.T) {
	t.Parallel()
	l := Loop{Name: "a", Every: "4m", NextRunAt: at(10, 0, 0)}
	assert.ErrorIs(t, l.Advance(at(10, 0, 0)), ErrInvalid)
}

func TestResume(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		next time.Time
		want time.Time
	}{
		{name: "overdue becomes now plus every", next: at(9, 0, 0), want: at(11, 0, 0)},
		{name: "future is unchanged", next: at(12, 0, 0), want: at(12, 0, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := Loop{Name: "a", Every: "1h", Paused: true, NextRunAt: tt.next}
			require.NoError(t, l.Resume(at(10, 0, 0)))
			assert.False(t, l.Paused)
			assert.True(t, tt.want.Equal(l.NextRunAt), "got %s", l.NextRunAt)
		})
	}
}

func TestAddRunKeepsNewest(t *testing.T) {
	t.Parallel()
	var l Loop
	total := MaxRuns + 3
	for i := range total {
		l.AddRun(Run{Workspace: fmt.Sprintf("ws-%02d", i)})
	}
	require.Len(t, l.Runs, MaxRuns)
	assert.Equal(t, "ws-03", l.Runs[0].Workspace)
	assert.Equal(t, fmt.Sprintf("ws-%02d", total-1), l.Runs[MaxRuns-1].Workspace)
}
