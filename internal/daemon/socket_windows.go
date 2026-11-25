//go:build windows

package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bobcob7/workspaces/internal/privfile"
)

// socketBaseDir is the directory of the socket on Windows, which is per user
// under %LocalAppData%\Temp (ADR 0081 D4).
func socketBaseDir(_ func(string) string, _ int) string {
	return filepath.Join(os.TempDir(), "ws")
}

// prepareSocketDir creates the socket's directory and restricts it to the
// current user; the directory's DACL is what keeps other users out.
func prepareSocketDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating the socket directory: %w", err)
	}
	if err := privfile.RestrictDir(dir); err != nil {
		return fmt.Errorf("socket directory %s must be private to you: %w", dir, err)
	}
	if err := privfile.CheckPrivate(dir); err != nil {
		return fmt.Errorf("socket directory %s must be private to you: %w", dir, err)
	}
	return nil
}
