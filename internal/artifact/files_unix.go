//go:build unix

package artifact

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// syncDirectory fsyncs dir. Filesystems that can't sync a directory are
// not an error: the rename is already as durable as they allow.
func syncDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening %s to sync it: %w", dir, err)
	}
	err = d.Sync()
	_ = d.Close()
	if err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, errors.ErrUnsupported) {
		return fmt.Errorf("syncing %s: %w", dir, err)
	}
	return nil
}
