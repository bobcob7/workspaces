package workspace

import (
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/bobcob7/workspaces/internal/lockfile"
)

// leftoverMinAge keeps CleanLeftovers away from a state directory another
// ws has only just made and not yet locked.
const leftoverMinAge = time.Minute

// CleanLeftovers removes the state that creation and removal can leave
// behind (ADR 0022): a state directory without workspace.toml, from a
// creation interrupted while cloning; and a record whose workspace
// directory doesn't exist, from a creation interrupted before its final
// rename, an interrupted removal, or a manual delete. State that's locked,
// or younger than a minute, may belong to a creation in progress and is
// kept. Failures are warnings. It returns the ids it removed.
func (s *Store) CleanLeftovers() []string {
	ids, err := stateIDs(s.stateDir)
	if err != nil {
		s.warn("couldn't check for leftovers: %v", err)
		return nil
	}
	var removed []string
	for _, id := range ids {
		if !s.isLeftover(id) {
			continue
		}
		if err := os.RemoveAll(s.stateOf(id)); err != nil {
			s.warn("couldn't remove leftover state %s: %v", id, err)
			continue
		}
		removed = append(removed, id)
	}
	return removed
}

func (s *Store) isLeftover(id string) bool {
	ws, err := readRecord(s.recordPath(id))
	switch {
	case errors.Is(err, errNoRecord):
		return s.abandoned(id)
	case err != nil:
		s.warn("leaving state %s alone: %v", id, err)
		return false
	}
	if _, err = os.Lstat(ws.Path); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return s.abandoned(id)
}

// abandoned reports whether workspace id's state is old enough and
// unlocked, so no creation or hand-off is still working on it. A lock that
// can't be checked counts as held.
func (s *Store) abandoned(id string) bool {
	info, err := os.Stat(s.stateOf(id))
	if err != nil || s.now().Sub(info.ModTime()) < leftoverMinAge {
		return false
	}
	held, err := lockfile.HeldExclusively(s.lockPath(id))
	return err == nil && !held
}
