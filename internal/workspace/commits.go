package workspace

import (
	"errors"
	"fmt"
)

// UsesCommit reports whether removing repo id's cached commit could affect
// a workspace: one records that commit, or a creation is in progress and
// may be about to check it out. A record that can't be read is an error,
// since that workspace might use the commit. It reads the state fresh on
// every call, so the cache can ask while holding the repo's lock.
func (s *Store) UsesCommit(id, commit string) (bool, error) {
	ids, err := stateIDs(s.stateDir)
	if err != nil {
		return false, err
	}
	for _, wsID := range ids {
		ws, err := readRecord(s.recordPath(wsID))
		if errors.Is(err, errNoRecord) {
			if !s.abandoned(wsID) {
				return true, nil
			}
			continue
		}
		if err != nil {
			return false, fmt.Errorf("a workspace record couldn't be read: %w", err)
		}
		for _, r := range ws.Repos {
			if r.ID == id && r.Commit == commit {
				return true, nil
			}
		}
	}
	return false, nil
}
