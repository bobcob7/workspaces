//go:build !darwin && !linux && !windows

package claude

import (
	"context"
	"errors"
	"fmt"
)

// startAttach fails: this system has no pseudo-terminals.
func (c *Client) startAttach(_ context.Context, id string) (attachTerm, error) {
	return nil, fmt.Errorf("opening a terminal for claude attach %s: %w", id, errors.ErrUnsupported)
}
