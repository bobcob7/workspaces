//go:build unix

package gate

import (
	"context"
	"os/exec"
)

func scriptCommand(ctx context.Context, script string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "/bin/sh", "-c", script), nil
}
