package workspace

import (
	"fmt"
	"strings"
	"unicode"
)

// Rename changes workspace id's display name, its record's name, to name
// with surrounding space trimmed. The id, and so its directory, never
// change (ADR 0036). A name that's empty, or holds a control character,
// is ErrNameInvalid.
func (s *Store) Rename(id, name string) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, fmt.Errorf("%w: a name can't be empty", ErrNameInvalid)
	}
	if err := checkName(name); err != nil {
		return Workspace{}, err
	}
	ws, err := s.byID(id)
	if err != nil {
		return Workspace{}, err
	}
	ws.Name = name
	if err := writeRecord(s.recordPath(id), &ws); err != nil {
		return Workspace{}, err
	}
	return ws, nil
}

// checkName rejects a display name with a control character, which would
// break ws list's table (ADR 0036).
func checkName(name string) error {
	if strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("%w: %q holds a control character", ErrNameInvalid, name)
	}
	return nil
}
