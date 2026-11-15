package claude

import (
	"context"
	"fmt"
)

// Logs is the text of session id's log, from `claude logs <id>`. An unknown
// id fails with ErrNoSession.
func (c *Client) Logs(ctx context.Context, id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("%w: %q", errBadID, id)
	}
	out, err := c.run(ctx, "logs", id)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
