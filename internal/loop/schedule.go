package loop

import (
	"fmt"
	"slices"
	"time"
)

// Due reports whether the loop should start a run at now: not paused, and
// NextRunAt is not after now.
func (l *Loop) Due(now time.Time) bool {
	return !l.Paused && !l.NextRunAt.After(now)
}

// Advance moves NextRunAt forward one interval after a run started or
// failed. If that is not after now, NextRunAt becomes now + interval.
func (l *Loop) Advance(now time.Time) error {
	interval, err := l.Interval()
	if err != nil {
		return fmt.Errorf("advancing loop %s: %w", l.Name, err)
	}
	next := l.NextRunAt.Add(interval)
	if !next.After(now) {
		next = now.Add(interval)
	}
	l.NextRunAt = next
	return nil
}

// Resume clears Paused; if NextRunAt is before now it becomes now + interval.
func (l *Loop) Resume(now time.Time) error {
	l.Paused = false
	if !l.NextRunAt.Before(now) {
		return nil
	}
	interval, err := l.Interval()
	if err != nil {
		return fmt.Errorf("resuming loop %s: %w", l.Name, err)
	}
	l.NextRunAt = now.Add(interval)
	return nil
}

// AddRun appends r and drops the oldest runs beyond MaxRuns.
//
//nolint:gocritic // hugeParam: the API takes a Run by value, as ADR 0048 specifies.
func (l *Loop) AddRun(r Run) {
	l.Runs = append(l.Runs, r)
	if excess := len(l.Runs) - MaxRuns; excess > 0 {
		l.Runs = slices.Clone(l.Runs[excess:])
	}
}
