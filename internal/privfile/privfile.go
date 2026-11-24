// Package privfile makes files and directories private to the current user
// (ADR 0081 D3). On unix the callers' mode bits already do that, so the
// functions only do work on Windows.
package privfile

import "errors"

// ErrNotPrivate means someone other than the current user can reach a path.
var ErrNotPrivate = errors.New("not private to the current user")
