package workspace

import (
	"errors"
)

var (
	// ErrNameInvalid means --name has no characters left after sanitizing
	// (ADR 0012).
	ErrNameInvalid = errors.New("invalid workspace name")
	// ErrExists means a workspace, or an unknown directory, already has the
	// id (ADR 0012).
	ErrExists = errors.New("workspace already exists")
	// ErrSpecConflict means two specs name the same repo, or two repos would
	// share a directory in the workspace.
	ErrSpecConflict = errors.New("conflicting repo specs")
	// ErrNotFound means no workspace matches an id or path.
	ErrNotFound = errors.New("workspace not found")
	// ErrUnsafe means the deletion guard refused a directory (ADR 0012).
	ErrUnsafe = errors.New("refusing to delete")
)
