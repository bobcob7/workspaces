package cache

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// excludeFromBackup asks Time Machine to skip dir, since cached repos can be
// cloned again (ADR 0011). It does nothing on other platforms.
func excludeFromBackup(ctx context.Context, dir string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	out, err := exec.CommandContext(ctx, "tmutil", "addexclusion", dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmutil addexclusion: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
