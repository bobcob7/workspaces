//go:build !darwin

package power

import "context"

// awake reports true: only macOS has dark wakes (ADR 0047).
func awake(context.Context) (bool, error) {
	return true, nil
}
